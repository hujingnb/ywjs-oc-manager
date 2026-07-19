"""验证本地演示数据脚本访问 manager API 时的认证与重试安全边界。"""

import json
import io
import os
import socket
import threading
import unittest
import urllib.error
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from unittest import mock

from local_seed_demo import client as client_module
from local_seed_demo.client import APIError, ManagerAPI, UncertainWrite


# 拆分测试凭据，避免异常 traceback 直接打印完整登录密码。
_LOGIN_PASSWORD = "admin" + "123"

# 拆分截断响应标记，避免 RED traceback 或断言源码直接暴露完整测试敏感值。
_PARTIAL_BODY_MARKER = "partial" + "-secret"


class _DeadlineClock:
    """为 HTTP deadline 测试提供可精确推进的单调时钟。"""

    def __init__(self):
        """从零开始记录退避实际消耗的虚拟秒数。"""
        self.now = 0.0
        self.sleeps = []

    def monotonic(self):
        """返回当前虚拟单调时间。"""
        return self.now

    def sleep(self, seconds):
        """记录并推进退避，不阻塞真实测试进程。"""
        self.sleeps.append(seconds)
        self.now += seconds


class _ScenarioHandler(BaseHTTPRequestHandler):
    """按测试场景提供最小 HTTP 行为，并只保留协议断言需要的请求信息。"""

    def log_message(self, format, *args):
        """关闭标准错误访问日志，避免失败输出夹带请求相关信息。"""

    def _record_request(self):
        """读取一次 JSON 请求并记录允许测试检查的四类信息。"""
        content_length = int(self.headers.get("Content-Length", "0"))
        raw_body = self.rfile.read(content_length) if content_length else b""
        json_body = json.loads(raw_body) if raw_body else None
        self.server.requests.append(
            {
                "method": self.command,
                "path": self.path,
                "authorization": self.headers.get("Authorization"),
                "json_body": json_body,
            }
        )

    def _send_json(self, status, payload):
        """发送带明确长度的 JSON，保证客户端能稳定判断响应是否完整。"""
        response_body = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(response_body)))
        self.end_headers()
        self.wfile.write(response_body)

    def do_GET(self):
        """提供认证读取和瞬时失败后恢复两条只读服务端分支。"""
        self._record_request()

        # 登录场景只允许读取助手版本，用于验证 Bearer token 会自动附加。
        if self.server.scenario == "login" and self.path == "/api/v1/assistant-versions":
            self._send_json(200, {"versions": [{"id": "v1"}]})
            return

        # 退避场景的第一次读取返回瞬时错误，验证客户端只进行一次有限重试。
        if self.server.scenario == "retry" and len(self.server.requests) == 1:
            self._send_json(503, {"code": "temporarily_unavailable", "message": "稍后重试"})
            return

        # 退避场景的第二次读取恢复，响应内容用于确认重试结果来自真实 HTTP 请求。
        if self.server.scenario == "retry" and len(self.server.requests) == 2:
            self._send_json(200, {"versions": [{"id": "v1"}]})
            return

        # 瞬时错误耗尽场景始终返回 503，用于锁定完整五档退避及最终安全错误。
        if self.server.scenario == "retry_exhausted":
            self._send_json(503, {"code": "temporarily_unavailable", "message": "稍后重试"})
            return

        # 连接错误场景在收到每次 GET 后立即断开，验证套接字异常也受有限退避约束。
        if self.server.scenario == "disconnect_get":
            self.close_connection = True
            self.connection.shutdown(socket.SHUT_RDWR)
            self.connection.close()
            return

        # 非瞬时客户端错误必须直接返回，不得因统一错误处理而误触发 GET 重试。
        if self.server.scenario == "bad_request":
            self._send_json(400, {"code": "bad_request", "message": "请求错误"})
            return

        # 合法 JSON null 不是错误对象，客户端应使用脱敏默认字段构造 APIError。
        if self.server.scenario == "error_null":
            self._send_json(500, None)
            return

        # 合法 JSON 数组同样不提供 code/message，不能因调用 dict 方法泄露内部异常。
        if self.server.scenario == "error_list":
            self._send_json(500, ["unexpected"])
            return

        # 截断错误场景声明更长响应体后只写部分 JSON，模拟代理或服务端中途断连。
        if self.server.scenario == "truncated_error":
            partial_body = f'{{"message":"{_PARTIAL_BODY_MARKER}'.encode("utf-8")
            self.send_response(500)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(partial_body) + 100))
            self.end_headers()
            self.wfile.write(partial_body)
            self.wfile.flush()
            self.close_connection = True
            self.connection.shutdown(socket.SHUT_RDWR)
            self.connection.close()
            return

        # 非测试契约内的路径统一返回 404，使意外请求立即暴露为测试失败。
        self._send_json(404, {"code": "not_found", "message": "不存在"})

    def do_POST(self):
        """提供登录成功和写入已到达但响应中断两条服务端分支。"""
        self._record_request()

        # 登录分支返回访问令牌与用户，令牌仅用于后续内存请求头断言。
        if self.server.scenario == "login" and self.path == "/api/v1/auth/login":
            self._send_json(
                200,
                {
                    "tokens": {"access_token": "token-1"},
                    "user": {"id": "admin-1"},
                },
            )
            return

        # 不确定写分支在完整接收请求后断开连接，模拟服务端可能已提交但未返回结果。
        if self.server.scenario == "uncertain_write" and self.path == "/api/v1/resources":
            self.close_connection = True
            self.connection.shutdown(socket.SHUT_RDWR)
            self.connection.close()
            return

        # 非测试契约内的路径统一返回 404，避免测试服务器静默接受错误写入。
        self._send_json(404, {"code": "not_found", "message": "不存在"})


class ManagerAPITest(unittest.TestCase):
    """通过本地真实套接字验证 manager 客户端的可观察协议行为。"""

    def setUp(self):
        """强制回环流量绕过开发机代理，确保断连语义来自测试服务器本身。"""
        previous_no_proxy = os.environ.get("no_proxy")
        os.environ["no_proxy"] = "127.0.0.1,localhost"

        # 清理时精确恢复调用前环境，避免测试对同进程内的其他用例产生副作用。
        def restore_no_proxy():
            if previous_no_proxy is None:
                os.environ.pop("no_proxy", None)
            else:
                os.environ["no_proxy"] = previous_no_proxy

        self.addCleanup(restore_no_proxy)

    def _start_server(self, scenario):
        """启动隔离的线程服务器，并由测试清理线程和监听端口。"""
        server = ThreadingHTTPServer(("127.0.0.1", 0), _ScenarioHandler)
        server.scenario = scenario
        server.requests = []
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(server.server_close)
        self.addCleanup(thread.join)
        self.addCleanup(server.shutdown)
        return server

    # 覆盖平台管理员无组织登录，并验证后续读取自动携带内存中的 Bearer token。
    def test_platform_admin_login_then_authenticated_get(self):
        server = self._start_server("login")
        base_url = f"http://127.0.0.1:{server.server_port}"
        client = ManagerAPI(base_url)

        user = client.login("", "admin", _LOGIN_PASSWORD)
        result = client.get("/api/v1/assistant-versions")

        self.assertEqual({"id": "admin-1"}, user)
        self.assertEqual([{"id": "v1"}], result["versions"])
        self.assertEqual(2, len(server.requests))
        login_request, versions_request = server.requests
        self.assertEqual("POST", login_request["method"])
        self.assertEqual("/api/v1/auth/login", login_request["path"])
        self.assertIsNone(login_request["authorization"])
        self.assertTrue(
            login_request["json_body"]
            == {"org_code": "", "username": "admin", "password": _LOGIN_PASSWORD},
            "登录请求体字段不符合认证契约",
        )
        self.assertEqual("GET", versions_request["method"])
        self.assertEqual("/api/v1/assistant-versions", versions_request["path"])
        self.assertEqual("Bearer token-1", versions_request["authorization"])
        self.assertIsNone(versions_request["json_body"])

    # 覆盖 GET 遇到一次 503 后按首档退避重试，且恢复后不产生额外请求。
    def test_get_retries_once_after_transient_503(self):
        server = self._start_server("retry")
        base_url = f"http://127.0.0.1:{server.server_port}"
        sleeps = []
        client = ManagerAPI(base_url, sleep=sleeps.append)

        result = client.get("/api/v1/assistant-versions")

        self.assertEqual([{"id": "v1"}], result["versions"])
        self.assertEqual([1], sleeps)
        self.assertEqual(2, len(server.requests))

    # 覆盖 GET 持续收到瞬时状态直至耗尽，锁定五档退避、六次请求和最终 APIError。
    def test_get_transient_status_exhausts_finite_backoff(self):
        server = self._start_server("retry_exhausted")
        base_url = f"http://127.0.0.1:{server.server_port}"
        sleeps = []
        client = ManagerAPI(base_url, sleep=sleeps.append)

        with self.assertRaises(APIError) as raised:
            client.get("/api/v1/assistant-versions")

        self.assertEqual([1, 2, 4, 8, 16], sleeps)
        self.assertEqual(6, len(server.requests))
        self.assertEqual(503, raised.exception.status)
        self.assertEqual("temporarily_unavailable", raised.exception.code)

    # 覆盖 GET 每次均遇到连接中断，要求有限退避耗尽后返回脱敏连接错误。
    def test_get_connection_error_exhausts_finite_backoff(self):
        server = self._start_server("disconnect_get")
        base_url = f"http://127.0.0.1:{server.server_port}"
        sleeps = []
        client = ManagerAPI(base_url, sleep=sleeps.append)

        with self.assertRaises(APIError) as raised:
            client.get("/api/v1/assistant-versions")

        self.assertEqual([1, 2, 4, 8, 16], sleeps)
        self.assertEqual(6, len(server.requests))
        self.assertIsNone(raised.exception.status)
        self.assertEqual("connection_error", raised.exception.code)

    # 覆盖 GET 的普通 4xx 错误，确认非瞬时状态只请求一次且不执行任何退避。
    def test_get_non_transient_4xx_does_not_retry(self):
        server = self._start_server("bad_request")
        base_url = f"http://127.0.0.1:{server.server_port}"
        sleeps = []
        client = ManagerAPI(base_url, sleep=sleeps.append)

        with self.assertRaises(APIError) as raised:
            client.get("/api/v1/assistant-versions")

        self.assertEqual([], sleeps)
        self.assertEqual(1, len(server.requests))
        self.assertEqual(400, raised.exception.status)
        self.assertEqual("bad_request", raised.exception.code)

    # 覆盖错误响应为 JSON null 的合法编码，要求降级为安全默认 APIError。
    def test_http_error_with_json_null_uses_safe_defaults(self):
        server = self._start_server("error_null")
        base_url = f"http://127.0.0.1:{server.server_port}"
        client = ManagerAPI(base_url)

        with self.assertRaises(APIError) as raised:
            client.get("/api/v1/assistant-versions")

        self.assertEqual("http_error", raised.exception.code)
        self.assertEqual("manager 请求失败", raised.exception.safe_message)

    # 覆盖错误响应为 JSON 数组的合法编码，要求忽略数组内容并使用安全默认字段。
    def test_http_error_with_json_list_uses_safe_defaults(self):
        server = self._start_server("error_list")
        base_url = f"http://127.0.0.1:{server.server_port}"
        client = ManagerAPI(base_url)

        with self.assertRaises(APIError) as raised:
            client.get("/api/v1/assistant-versions")

        self.assertEqual("http_error", raised.exception.code)
        self.assertEqual("manager 请求失败", raised.exception.safe_message)

    # 覆盖错误响应按 Content-Length 读取时中途截断，要求转为安全默认 APIError。
    def test_truncated_http_error_body_uses_safe_defaults(self):
        server = self._start_server("truncated_error")
        base_url = f"http://127.0.0.1:{server.server_port}"
        client = ManagerAPI(base_url)

        with self.assertRaises(APIError) as raised:
            client.get("/api/v1/assistant-versions")

        self.assertEqual(1, len(server.requests))
        self.assertEqual(500, raised.exception.status)
        self.assertEqual("http_error", raised.exception.code)
        self.assertEqual("manager 请求失败", raised.exception.safe_message)
        self.assertTrue(
            _PARTIAL_BODY_MARKER not in str(raised.exception),
            "截断的部分错误响应不得进入异常文本",
        )

    # 覆盖调用方可将 API 与不确定写错误统一视作运行时失败进行捕获的继承契约。
    def test_client_errors_inherit_runtime_error(self):
        self.assertTrue(issubclass(APIError, RuntimeError))
        self.assertTrue(issubclass(UncertainWrite, RuntimeError))
        self.assertTrue(
            issubclass(client_module.RequestDeadlineExceeded, RuntimeError)
        )

    # 覆盖写请求已送达却无响应的歧义状态，要求抛错且绝不盲目重放 POST。
    def test_post_disconnect_after_arrival_raises_uncertain_write_without_retry(self):
        server = self._start_server("uncertain_write")
        base_url = f"http://127.0.0.1:{server.server_port}"
        client = ManagerAPI(base_url)

        with self.assertRaises(UncertainWrite) as raised:
            client.post("/api/v1/resources", {"name": "demo"}, authenticated=False)

        self.assertEqual(1, len(server.requests))
        self.assertNotIn("demo", str(raised.exception))

    # 覆盖亚秒剩余预算：urlopen timeout 取客户端上限与 remaining 的较小正值。
    def test_get_deadline_limits_urlopen_timeout(self):
        clock = _DeadlineClock()
        response = io.BytesIO(b'{"ok": true}')
        client = ManagerAPI(
            "http://manager.test",
            sleep=clock.sleep,
            timeout=60,
            monotonic=clock.monotonic,
        )

        with mock.patch("urllib.request.urlopen", return_value=response) as urlopen:
            self.assertEqual({"ok": True}, client.get("/healthz", deadline=0.25))

        self.assertEqual(1, urlopen.call_count)
        self.assertGreater(urlopen.call_args.kwargs["timeout"], 0)
        self.assertAlmostEqual(0.25, urlopen.call_args.kwargs["timeout"])

    # 覆盖瞬时错误后的退避超过剩余预算：sleep 截断，耗尽后不得发送第二次 GET。
    def test_get_deadline_truncates_backoff_and_forbids_retry(self):
        clock = _DeadlineClock()
        error = urllib.error.HTTPError(
            "http://manager.test/healthz",
            503,
            "unavailable",
            None,
            io.BytesIO(b'{"code":"temporary"}'),
        )
        client = ManagerAPI(
            "http://manager.test",
            sleep=clock.sleep,
            monotonic=clock.monotonic,
        )

        with mock.patch("urllib.request.urlopen", side_effect=error) as urlopen:
            with self.assertRaises(client_module.RequestDeadlineExceeded):
                client.get("/healthz", deadline=0.25)

        self.assertEqual([0.25], clock.sleeps)
        self.assertEqual(1, urlopen.call_count)

    # 覆盖调用前预算已经耗尽：不得构造网络副作用，也不得进入退避 busy loop。
    def test_get_exhausted_deadline_sends_no_request(self):
        clock = _DeadlineClock()
        clock.now = 10.0
        client = ManagerAPI(
            "http://manager.test",
            sleep=clock.sleep,
            monotonic=clock.monotonic,
        )

        with mock.patch("urllib.request.urlopen") as urlopen:
            with self.assertRaises(client_module.RequestDeadlineExceeded):
                client.get("/healthz", deadline=10.0)

        urlopen.assert_not_called()
        self.assertEqual([], clock.sleeps)


if __name__ == "__main__":
    unittest.main()
