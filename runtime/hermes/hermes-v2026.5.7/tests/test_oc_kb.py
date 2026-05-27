"""验证 oc-kb CLI 的本地错误路径。"""

import json
import os
import subprocess
import sys
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path


def _start_runtime_server(response: dict):
    # 用本地 HTTP server 捕获 oc-kb 发出的真实请求，避免把 manager runtime API 调用 mock 掉。
    records: list[dict] = []

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self) -> None:
            length = int(self.headers.get("Content-Length", "0"))
            body = self.rfile.read(length)
            records.append({
                "path": self.path,
                "token": self.headers.get("X-OC-App-Token"),
                "content_type": self.headers.get("Content-Type", ""),
                "body": body,
            })
            payload = json.dumps(response).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        def log_message(self, _format: str, *_args) -> None:
            return

    server = HTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server, thread, records


def _stop_runtime_server(server: HTTPServer, thread: threading.Thread) -> None:
    server.shutdown()
    server.server_close()
    thread.join(timeout=1)


def _runtime_env(server: HTTPServer, **extra: str) -> dict[str, str]:
    env = os.environ.copy()
    env.update({
        "OC_KB_RUNTIME_BASE_URL": f"http://127.0.0.1:{server.server_port}",
        "OC_KB_APP_TOKEN": "runtime-token",
    })
    env.update(extra)
    return env


def test_oc_kb_search_posts_to_manager_runtime_api() -> None:
    # search 成功路径必须只调用 manager runtime API，并把后端响应包成稳定的 ok/data stdout。
    script = Path(__file__).resolve().parent.parent / "oc-kb.py"
    server, thread, records = _start_runtime_server({"results": [{"content": "answer"}]})

    try:
        result = subprocess.run(
            [sys.executable, str(script), "search", "退款政策", "--top-k", "3"],
            env=_runtime_env(server),
            capture_output=True,
            text=True,
        )
    finally:
        _stop_runtime_server(server, thread)

    assert result.returncode == 0
    assert json.loads(result.stdout) == {"ok": True, "data": {"results": [{"content": "answer"}]}}
    assert len(records) == 1
    assert records[0]["path"] == "/api/v1/runtime/knowledge/search"
    assert records[0]["token"] == "runtime-token"
    assert json.loads(records[0]["body"].decode("utf-8")) == {"question": "退款政策", "top_k": 3}


def test_oc_kb_add_posts_workspace_file_to_manager_runtime_api(tmp_path: Path) -> None:
    # add 成功路径必须上传 workspace 内文件到 manager runtime API，并携带 app token。
    script = Path(__file__).resolve().parent.parent / "oc-kb.py"
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    (workspace / "report.md").write_text("hello", encoding="utf-8")
    server, thread, records = _start_runtime_server({"id": "doc-1", "name": "report.md"})

    try:
        result = subprocess.run(
            [sys.executable, str(script), "add", "report.md"],
            env=_runtime_env(server, OC_WORKSPACE_DIR=str(workspace)),
            capture_output=True,
            text=True,
        )
    finally:
        _stop_runtime_server(server, thread)

    assert result.returncode == 0
    assert json.loads(result.stdout) == {"ok": True, "data": {"id": "doc-1", "name": "report.md"}}
    assert len(records) == 1
    assert records[0]["path"] == "/api/v1/runtime/knowledge/files"
    assert records[0]["token"] == "runtime-token"
    assert records[0]["content_type"].startswith("multipart/form-data; boundary=")
    assert b'name="file"; filename="report.md"' in records[0]["body"]
    assert b"hello" in records[0]["body"]


def test_oc_kb_requires_runtime_config(monkeypatch) -> None:
    # 缺少 manager runtime API 配置时，oc-kb 应快速失败，避免 Hermes 误以为知识库为空。
    monkeypatch.delenv("OC_KB_RUNTIME_BASE_URL", raising=False)
    monkeypatch.delenv("OC_KB_APP_TOKEN", raising=False)
    script = Path(__file__).resolve().parent.parent / "oc-kb.py"

    result = subprocess.run(
        [sys.executable, str(script), "search", "hello"],
        env={k: v for k, v in os.environ.items() if not k.startswith("OC_KB_")},
        capture_output=True,
        text=True,
    )

    assert result.returncode == 1
    assert "not configured" in result.stderr


def test_oc_kb_add_rejects_path_outside_workspace(tmp_path: Path) -> None:
    # 父目录穿越不能越过 workspace 沙箱，避免 Hermes 写入任意本地文件到实例知识库。
    script = Path(__file__).resolve().parent.parent / "oc-kb.py"
    workspace = tmp_path / "workspace"
    workspace.mkdir()

    env = os.environ.copy()
    env.update({
        "OC_KB_RUNTIME_BASE_URL": "http://manager-api:8080",
        "OC_KB_APP_TOKEN": "runtime-token",
        "OC_WORKSPACE_DIR": str(workspace),
    })
    result = subprocess.run(
        [sys.executable, str(script), "add", "../secret.md"],
        env=env,
        capture_output=True,
        text=True,
    )

    assert result.returncode == 1
    assert "INVALID_PATH" in result.stderr
    assert "workspace" in result.stderr
