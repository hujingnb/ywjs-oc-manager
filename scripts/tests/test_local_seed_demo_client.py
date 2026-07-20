"""验证本地演示数据脚本访问 manager API 时的认证与重试安全边界。"""

import json
import io
import os
import socket
import threading
import time
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


class _ReadOnlyResponse:
    """模拟仅实现 read 的最小 file-like 响应，验证非 HTTPResponse 回退路径。"""

    def __init__(self, body):
        """保存响应字节，并提供 urlopen 上下文管理器所需的关闭语义。"""
        self.body = io.BytesIO(body)

    def read(self, size=-1):
        """按调用方指定大小读取，刻意不提供 HTTPResponse.read1。"""
        return self.body.read(size)

    def __enter__(self):
        """返回当前响应供 with 块读取。"""
        return self

    def __exit__(self, exc_type, exc_value, traceback):
        """退出 with 块时关闭底层字节流。"""
        self.body.close()


class _CompletionResponse(_ReadOnlyResponse):
    """在关闭时发出信号，供阻塞 urlopen 测试等待 worker 清理响应。"""

    def __init__(self, body, response_closed):
        """保存响应字节和响应已关闭的同步事件。"""
        super().__init__(body)
        self.response_closed = response_closed

    def __exit__(self, exc_type, exc_value, traceback):
        """关闭响应后通知测试，避免 daemon worker 仍依赖即将撤销的 mock。"""
        super().__exit__(exc_type, exc_value, traceback)
        self.response_closed.set()


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

    def _send_bytes(self, status, content_type, response_body):
        """发送测试指定的原始响应体，用于覆盖成功状态下的非法 JSON 编码。"""
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(response_body)))
        self.end_headers()
        self.wfile.write(response_body)

    def do_GET(self):
        """提供健康检查、认证读取和瞬时失败恢复等只读服务端分支。"""
        self._record_request()

        # 正常健康场景用于独立验证固定路径、匿名请求和默认 deadline。
        if self.server.scenario == "health_ok":
            self._send_json(200, {"status": "ok"})
            return

        # 健康稳定性场景前两次成功，验证尚未达到连续三次门槛时不能提前返回。
        if (
            self.server.scenario == "health_retry"
            and len(self.server.requests) in {1, 2}
        ):
            self._send_json(200, {"status": "ok"})
            return

        # 第三次模拟 Traefik 路由切换返回 502，连续成功计数必须清零。
        if (
            self.server.scenario == "health_retry"
            and len(self.server.requests) == 3
        ):
            self._send_json(502, {"code": "bad_gateway", "message": "入口尚未就绪"})
            return

        # 第四至六次恢复，只有重新连续成功三次才允许后续登录写请求。
        if (
            self.server.scenario == "health_retry"
            and len(self.server.requests) in {4, 5, 6}
        ):
            self._send_json(200, {"status": "ok"})
            return

        # 健康检查返回非 ok 状态时仍属于无效响应，不应继续轮询或发起登录。
        if self.server.scenario == "health_invalid":
            self._send_json(200, {"status": "starting"})
            return

        # HTML 成功响应不属于 manager JSON 契约，客户端必须转为安全 APIError。
        if self.server.scenario == "health_html":
            self._send_bytes(200, "text/html", b"<html>bad gateway</html>")
            return

        # 语法截断的 JSON 即使 HTTP 200，也不能让 JSONDecodeError 逃出客户端边界。
        if self.server.scenario == "health_truncated_json":
            self._send_bytes(200, "application/json", b'{"status":"ok"')
            return

        # 非法 UTF-8 JSON 必须与其他成功响应解析失败使用相同的安全错误。
        if self.server.scenario == "health_invalid_utf8":
            self._send_bytes(200, "application/json", b'{"status":"\xff"}')
            return

        # 每 50ms 仅发送一个字节，复现单次 socket timeout 被持续小包续命的问题。
        if self.server.scenario == "health_slow_trickle":
            response_body = b'{"status":"ok"}'
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(response_body)))
            self.end_headers()
            for byte in response_body:
                time.sleep(0.05)
                try:
                    self.wfile.write(bytes([byte]))
                    self.wfile.flush()
                except (BrokenPipeError, ConnectionResetError):
                    return
            return

        # 有效 JSON 体故意短于声明长度，验证 EOF 时仍会执行 Content-Length 校验。
        if self.server.scenario == "health_short_content_length":
            response_body = b'{"status":"ok"}'
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(response_body) + 100))
            self.end_headers()
            self.wfile.write(response_body)
            self.wfile.flush()
            self.close_connection = True
            self.connection.shutdown(socket.SHUT_RDWR)
            self.connection.close()
            return

        # 非数字 Content-Length 不具备可验证的消息边界，必须作为非法响应拒绝。
        if self.server.scenario == "health_invalid_content_length":
            response_body = b'{"status":"ok"}'
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", "invalid")
            self.end_headers()
            self.wfile.write(response_body)
            return

        # 两个冲突 Content-Length 会形成代理解析歧义，即使 body 合法也必须拒绝。
        if self.server.scenario == "health_conflicting_content_length":
            response_body = b'{"status":"ok"}'
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(response_body)))
            self.send_header("Content-Length", str(len(response_body) + 1))
            self.end_headers()
            self.wfile.write(response_body)
            return

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

    def do_PUT(self):
        """模拟已接收 AICC 配置更新后响应中断的真实写入链路。"""
        self._record_request()

        # PUT 与 POST 一样可能在服务端处理成功后断连，客户端必须交给 seeder 回查。
        if (
            self.server.scenario == "uncertain_aicc_put"
            and self.path == "/api/v1/organizations/org-1/aicc-config"
        ):
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

    # 覆盖连续两次成功后出现 502，要求稳定计数清零并重新连续成功三次。
    def test_wait_ready_resets_stability_after_transient_502(self):
        server = self._start_server("health_retry")
        base_url = f"http://127.0.0.1:{server.server_port}"
        sleeps = []
        client = ManagerAPI(base_url, sleep=sleeps.append)

        result = client.wait_ready()

        self.assertEqual({"status": "ok"}, result)
        self.assertEqual([1, 1, 1, 1, 1], sleeps)
        self.assertEqual(
            ["GET", "GET", "GET", "GET", "GET", "GET"],
            [request["method"] for request in server.requests],
        )
        self.assertEqual(
            ["/healthz"] * 6,
            [request["path"] for request in server.requests],
        )

    # 覆盖健康接口返回非 ok 业务状态，要求立即拒绝且不重复健康检查。
    def test_wait_ready_rejects_non_ok_envelope(self):
        server = self._start_server("health_invalid")
        base_url = f"http://127.0.0.1:{server.server_port}"
        client = ManagerAPI(base_url)

        with self.assertRaises(APIError) as raised:
            client.wait_ready()

        self.assertEqual(200, raised.exception.status)
        self.assertEqual("invalid_health_response", raised.exception.code)
        self.assertEqual(1, len(server.requests))
        self.assertEqual("GET", server.requests[0]["method"])
        self.assertEqual("/healthz", server.requests[0]["path"])

    # 覆盖客户端已有访问令牌时的健康检查，固定路径仍必须匿名且不泄露 Bearer token。
    def test_wait_ready_uses_anonymous_health_path(self):
        server = self._start_server("health_ok")
        base_url = f"http://127.0.0.1:{server.server_port}"
        sleeps = []
        client = ManagerAPI(base_url, sleep=sleeps.append)
        client.access_token = "existing-token"

        self.assertEqual({"status": "ok"}, client.wait_ready())

        self.assertEqual([1, 1], sleeps)
        self.assertEqual(3, len(server.requests))
        self.assertTrue(
            all(request["method"] == "GET" for request in server.requests)
        )
        self.assertTrue(
            all(request["path"] == "/healthz" for request in server.requests)
        )
        self.assertTrue(
            all(request["authorization"] is None for request in server.requests)
        )

    # 覆盖 wait_ready 默认总预算，要求首次请求得到约 60 秒剩余 timeout。
    def test_wait_ready_uses_default_sixty_second_deadline(self):
        clock = _DeadlineClock()
        responses = [io.BytesIO(b'{"status":"ok"}') for _ in range(3)]
        client = ManagerAPI(
            "http://manager.test",
            sleep=clock.sleep,
            monotonic=clock.monotonic,
        )

        with mock.patch("urllib.request.urlopen", side_effect=responses) as urlopen:
            self.assertEqual({"status": "ok"}, client.wait_ready())

        self.assertEqual([1, 1], clock.sleeps)
        self.assertEqual(3, urlopen.call_count)
        self.assertEqual(
            [60, 59, 58],
            [call.kwargs["timeout"] for call in urlopen.call_args_list],
        )

    # 覆盖持续小包响应不能续命：0.2 秒绝对预算应在宽松的 0.5 秒上限内中止读取。
    def test_wait_ready_deadline_interrupts_slow_trickle_response(self):
        server = self._start_server("health_slow_trickle")
        base_url = f"http://127.0.0.1:{server.server_port}"
        client = ManagerAPI(base_url)
        started_at = time.monotonic()

        with self.assertRaises(client_module.RequestDeadlineExceeded):
            client.wait_ready(timeout=0.2)

        elapsed = time.monotonic() - started_at
        self.assertLess(elapsed, 0.5)
        self.assertEqual(1, len(server.requests))

    # 覆盖 DNS/urlopen 建连前阻塞：外层墙钟门禁必须在 0.2 秒预算后返回。
    def test_wait_ready_deadline_interrupts_blocked_urlopen(self):
        response_closed = threading.Event()
        response = _CompletionResponse(b'{"status":"ok"}', response_closed)

        def blocked_urlopen(*args, **kwargs):
            """模拟 DNS 阶段不理会 socket timeout，并在 0.5 秒后才返回响应。"""
            time.sleep(0.5)
            return response

        client = ManagerAPI("http://manager.test")
        with mock.patch("urllib.request.urlopen", side_effect=blocked_urlopen):
            started_at = time.monotonic()
            with self.assertRaises(client_module.RequestDeadlineExceeded):
                client.wait_ready(timeout=0.2)
            elapsed = time.monotonic() - started_at
            self.assertLess(elapsed, 0.5)
            self.assertTrue(response_closed.wait(1))
            # 响应关闭后 worker 只剩异常入队，退出 patch 前仍显式等待其结束。
            worker_deadline = time.monotonic() + 1
            while time.monotonic() < worker_deadline:
                health_workers = [
                    thread
                    for thread in threading.enumerate()
                    if thread.name == "manager-health-check" and thread.is_alive()
                ]
                if not health_workers:
                    break
                time.sleep(0.01)
            self.assertEqual([], health_workers)

    # 覆盖可注入时钟停滞时的多轮探测，真实墙钟总预算仍须在 0.2 秒内中止。
    def test_wait_ready_enforces_wall_deadline_when_injected_clock_stalls(self):
        request_count = 0
        second_probe_completed = threading.Event()

        def frozen_monotonic():
            """模拟不会随真实请求耗时推进的测试单调时钟。"""
            return 0

        def slow_health_request(*args, **kwargs):
            """每次探测真实阻塞 0.08 秒，复现逐轮重复授予完整预算的问题。"""
            nonlocal request_count
            request_count += 1
            current_request = request_count
            time.sleep(0.08)
            # 墙钟超时发生在第二轮中途；完成信号用于退出 mock 前清理该 daemon。
            if current_request == 2:
                second_probe_completed.set()
            return {"status": "ok"}

        client = ManagerAPI(
            "http://manager.test",
            sleep=lambda _: None,
            monotonic=frozen_monotonic,
        )

        started_at = time.monotonic()
        with mock.patch.object(client, "_request", side_effect=slow_health_request):
            with self.assertRaises(client_module.RequestDeadlineExceeded):
                client.wait_ready(timeout=0.1)
            elapsed = time.monotonic() - started_at
            self.assertTrue(second_probe_completed.wait(0.2))
            # side effect 返回后还需等待 worker 完成入队，确保撤销 mock 时无线程残留。
            worker_deadline = time.monotonic() + 0.2
            while time.monotonic() < worker_deadline:
                health_workers = [
                    thread
                    for thread in threading.enumerate()
                    if thread.name == "manager-health-check" and thread.is_alive()
                ]
                if not health_workers:
                    break
                time.sleep(0.001)
            self.assertEqual([], health_workers)

        self.assertLess(elapsed, 0.2)

    # 覆盖探测耗时与稳定间隔共享墙钟预算，冻结注入时钟不能让间隔越过 0.05 秒上限。
    def test_wait_ready_wall_deadline_includes_probe_interval(self):
        def frozen_monotonic():
            """模拟内部 deadline 不会随探测和真实休眠推进。"""
            return 0

        def slow_health_request(*args, **kwargs):
            """单次探测消耗大部分墙钟预算，剩余时间不足完整稳定间隔。"""
            time.sleep(0.04)
            return {"status": "ok"}

        client = ManagerAPI(
            "http://manager.test",
            monotonic=frozen_monotonic,
        )

        started_at = time.monotonic()
        with mock.patch.object(client, "_request", side_effect=slow_health_request):
            with self.assertRaises(client_module.RequestDeadlineExceeded):
                client.wait_ready(timeout=0.05)
        elapsed = time.monotonic() - started_at

        # 主线程按墙钟退出后等待已截断的间隔 worker 收尾，避免跨测试残留 daemon。
        worker_deadline = time.monotonic() + 0.1
        while time.monotonic() < worker_deadline:
            interval_workers = [
                thread
                for thread in threading.enumerate()
                if thread.name == "manager-health-interval" and thread.is_alive()
            ]
            if not interval_workers:
                break
            time.sleep(0.001)

        self.assertLess(elapsed, 0.08)
        self.assertEqual([], interval_workers)

    # 覆盖 daemon worker 返回 APIError，主线程必须原样重新抛出同一异常对象。
    def test_wait_ready_reraises_worker_api_error(self):
        expected = APIError("GET /healthz", 200, "invalid_response", "响应异常")
        client = ManagerAPI("http://manager.test")

        with mock.patch.object(client, "_request", side_effect=expected):
            with self.assertRaises(APIError) as raised:
                client.wait_ready()

        self.assertIs(expected, raised.exception)

    # 覆盖 worker 收到 KeyboardInterrupt，必须立即搬运至主线程且不触发线程 traceback。
    def test_wait_ready_reraises_worker_keyboard_interrupt_immediately(self):
        expected = KeyboardInterrupt()
        client = ManagerAPI("http://manager.test")

        with mock.patch.object(client, "_request", side_effect=expected):
            with mock.patch("threading.excepthook") as excepthook:
                started_at = time.monotonic()
                with self.assertRaises(KeyboardInterrupt) as raised:
                    client.wait_ready(timeout=0.2)
                elapsed = time.monotonic() - started_at

        self.assertIs(expected, raised.exception)
        self.assertLess(elapsed, 0.5)
        excepthook.assert_not_called()

    # 覆盖 worker 收到 SystemExit，必须保留退出码立即传播且不打印线程 traceback。
    def test_wait_ready_reraises_worker_system_exit_immediately(self):
        expected = SystemExit(7)
        client = ManagerAPI("http://manager.test")

        with mock.patch.object(client, "_request", side_effect=expected):
            with mock.patch("threading.excepthook") as excepthook:
                started_at = time.monotonic()
                with self.assertRaises(SystemExit) as raised:
                    client.wait_ready(timeout=0.2)
                elapsed = time.monotonic() - started_at

        self.assertIs(expected, raised.exception)
        self.assertEqual(7, raised.exception.code)
        self.assertLess(elapsed, 0.5)
        excepthook.assert_not_called()

    # 覆盖 daemon worker 连续三次成功后才返回，并在两次探测之间固定等待一秒。
    def test_wait_ready_requires_three_consecutive_successes(self):
        sleeps = []
        client = ManagerAPI("http://manager.test", sleep=sleeps.append)

        with mock.patch.object(
            client,
            "_request",
            return_value={"status": "ok"},
        ) as request:
            result = client.wait_ready(timeout=60)

        self.assertEqual({"status": "ok"}, result)
        self.assertEqual([1, 1], sleeps)
        self.assertEqual(3, request.call_count)
        self.assertTrue(
            all(
                call.kwargs["authenticated"] is False
                for call in request.call_args_list
            )
        )

    # 覆盖 HTTP 200 HTML，要求映射为固定安全字段而非暴露 JSON 解析异常或响应体。
    def test_success_html_response_raises_safe_api_error(self):
        server = self._start_server("health_html")
        base_url = f"http://127.0.0.1:{server.server_port}"
        client = ManagerAPI(base_url)

        with self.assertRaises(APIError) as raised:
            client.wait_ready()

        self.assertEqual(200, raised.exception.status)
        self.assertEqual("invalid_response", raised.exception.code)
        self.assertEqual("manager 响应格式异常", raised.exception.safe_message)
        self.assertNotIn("bad gateway", str(raised.exception))

    # 覆盖 HTTP 200 截断 JSON，要求复用成功响应的安全解析错误边界。
    def test_success_truncated_json_raises_safe_api_error(self):
        server = self._start_server("health_truncated_json")
        base_url = f"http://127.0.0.1:{server.server_port}"
        client = ManagerAPI(base_url)

        with self.assertRaises(APIError) as raised:
            client.wait_ready()

        self.assertEqual(200, raised.exception.status)
        self.assertEqual("invalid_response", raised.exception.code)
        self.assertEqual("manager 响应格式异常", raised.exception.safe_message)

    # 覆盖 HTTP 200 非法 UTF-8 JSON，要求复用成功响应的安全解析错误边界。
    def test_success_invalid_utf8_raises_safe_api_error(self):
        server = self._start_server("health_invalid_utf8")
        base_url = f"http://127.0.0.1:{server.server_port}"
        client = ManagerAPI(base_url)

        with self.assertRaises(APIError) as raised:
            client.wait_ready()

        self.assertEqual(200, raised.exception.status)
        self.assertEqual("invalid_response", raised.exception.code)
        self.assertEqual("manager 响应格式异常", raised.exception.safe_message)

    # 覆盖有效健康 JSON 短于声明长度，EOF 后必须安全拒绝而非接受 status=ok。
    def test_success_short_content_length_raises_safe_api_error(self):
        server = self._start_server("health_short_content_length")
        base_url = f"http://127.0.0.1:{server.server_port}"
        client = ManagerAPI(base_url)

        with self.assertRaises(APIError) as raised:
            client.wait_ready()

        self.assertEqual(200, raised.exception.status)
        self.assertEqual("invalid_response", raised.exception.code)
        self.assertEqual("manager 响应格式异常", raised.exception.safe_message)

    # 覆盖非数字 Content-Length，urllib 接受响应头时客户端仍须安全拒绝。
    def test_success_invalid_content_length_raises_safe_api_error(self):
        server = self._start_server("health_invalid_content_length")
        base_url = f"http://127.0.0.1:{server.server_port}"
        client = ManagerAPI(base_url)

        with self.assertRaises(APIError) as raised:
            client.wait_ready()

        self.assertEqual(200, raised.exception.status)
        self.assertEqual("invalid_response", raised.exception.code)

    # 覆盖冲突 Content-Length，urllib 保留重复头时客户端须拒绝代理解析歧义。
    def test_success_conflicting_content_length_raises_safe_api_error(self):
        server = self._start_server("health_conflicting_content_length")
        base_url = f"http://127.0.0.1:{server.server_port}"
        client = ManagerAPI(base_url)

        with self.assertRaises(APIError) as raised:
            client.wait_ready()

        self.assertEqual(200, raised.exception.status)
        self.assertEqual("invalid_response", raised.exception.code)

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

    # 覆盖非瞬时 HTTPError 的终态分支，解析安全错误后必须关闭底层响应流。
    def test_non_transient_http_error_closes_response(self):
        error_body = io.BytesIO(b'{"code":"bad_request"}')
        error = urllib.error.HTTPError(
            "http://manager.test/resource",
            400,
            "bad request",
            None,
            error_body,
        )
        client = ManagerAPI("http://manager.test")

        with mock.patch("urllib.request.urlopen", side_effect=error):
            with self.assertRaises(APIError):
                client.get("/resource")

        self.assertTrue(error_body.closed)

    # 覆盖瞬时 HTTPError 耗尽后的终态分支，包含最后一次在内的所有错误流均须关闭。
    def test_exhausted_transient_http_errors_close_all_responses(self):
        error_bodies = [io.BytesIO(b'{"code":"temporary"}') for _ in range(6)]
        errors = [
            urllib.error.HTTPError(
                "http://manager.test/resource",
                503,
                "unavailable",
                None,
                error_body,
            )
            for error_body in error_bodies
        ]
        client = ManagerAPI("http://manager.test", sleep=lambda _: None)

        with mock.patch("urllib.request.urlopen", side_effect=errors):
            with self.assertRaises(APIError):
                client.get("/resource")

        self.assertTrue(all(error_body.closed for error_body in error_bodies))

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

    # 覆盖 AICC PUT 已到达服务端但响应中断，要求转换为 seeder 可回查的不确定写入。
    def test_put_disconnect_after_arrival_raises_uncertain_write_without_retry(self):
        server = self._start_server("uncertain_aicc_put")
        base_url = f"http://127.0.0.1:{server.server_port}"
        client = ManagerAPI(base_url)

        with self.assertRaises(UncertainWrite) as raised:
            client.put(
                "/api/v1/organizations/org-1/aicc-config",
                {"enabled": True, "model": "demo-model"},
            )

        self.assertEqual(1, len(server.requests))
        self.assertEqual("PUT", server.requests[0]["method"])
        self.assertEqual(
            {"enabled": True, "model": "demo-model"},
            server.requests[0]["json_body"],
        )
        self.assertNotIn("demo-model", str(raised.exception))

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

    # 覆盖测试替身等仅实现 read 的 file-like 响应，deadline 读取器必须安全回退。
    def test_get_deadline_reader_falls_back_without_read1(self):
        clock = _DeadlineClock()
        response = _ReadOnlyResponse(b'{"ok":true}')
        client = ManagerAPI(
            "http://manager.test",
            monotonic=clock.monotonic,
        )

        with mock.patch("urllib.request.urlopen", return_value=response):
            result = client.get("/healthz", deadline=1)

        self.assertEqual({"ok": True}, result)
        self.assertTrue(response.body.closed)

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
