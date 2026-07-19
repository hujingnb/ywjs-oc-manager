"""验证本地演示数据脚本访问 manager API 时的认证与重试安全边界。"""

import json
import os
import socket
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from local_seed_demo.client import ManagerAPI, UncertainWrite


# 拆分测试凭据，避免异常 traceback 直接打印完整登录密码。
_LOGIN_PASSWORD = "admin" + "123"


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
            self._send_json(200, {"data": {"versions": [{"id": "v1"}]}})
            return

        # 退避场景的第一次读取返回瞬时错误，验证客户端只进行一次有限重试。
        if self.server.scenario == "retry" and len(self.server.requests) == 1:
            self._send_json(503, {"code": "temporarily_unavailable", "message": "稍后重试"})
            return

        # 退避场景的第二次读取恢复，响应内容用于确认重试结果来自真实 HTTP 请求。
        if self.server.scenario == "retry" and len(self.server.requests) == 2:
            self._send_json(200, {"data": {"versions": [{"id": "v1"}]}})
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
                    "data": {
                        "tokens": {"access_token": "token-1"},
                        "user": {"id": "admin-1"},
                    }
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

    # 覆盖写请求已送达却无响应的歧义状态，要求抛错且绝不盲目重放 POST。
    def test_post_disconnect_after_arrival_raises_uncertain_write_without_retry(self):
        server = self._start_server("uncertain_write")
        base_url = f"http://127.0.0.1:{server.server_port}"
        client = ManagerAPI(base_url)

        with self.assertRaises(UncertainWrite) as raised:
            client.post("/api/v1/resources", {"name": "demo"}, authenticated=False)

        self.assertEqual(1, len(server.requests))
        self.assertNotIn("demo", str(raised.exception))


if __name__ == "__main__":
    unittest.main()
