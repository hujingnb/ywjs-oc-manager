# tests/test_ocops_server_sse.py
"""覆盖 server 两个 SSE 端点：channel login 事件流（mock qr_login）逐帧、
kanban watch 事件流（monkeypatch watch_events）逐帧与 error 帧。

SSE 协议断言要点：data 帧形如 `data: <json>\n\n`；watch 启动失败时
应发 `event: error\ndata: <json>\n\n` 帧而非中断连接（让前端能读到错误体）。
"""
import json
import sys
import types

from jsonschema import validate
from starlette.testclient import TestClient


def _client(monkeypatch):
    # 构造带固定 token 的测试 client；SSE 端点不依赖 OC_DATA_DIR/OC_INFO_FILE。
    monkeypatch.setenv("OC_OPS_TOKEN", "t0ken")
    from ocops.server import app
    return TestClient(app)


# Bearer 头：所有 SSE 端点同样走 AuthMiddleware，需带正确 token 才能进入流。
_AUTH = {"Authorization": "Bearer t0ken"}


def _parse_sse(text: str):
    """把 SSE 文本拆成事件帧列表，每帧为 {"event": <名或 None>, "data": <解码 json>}。

    手写解析：以空行分隔帧，逐帧收集 `event:` / `data:` 行；data 行的 json
    在此解码，便于断言事件内容而不纠结原始字节。
    """
    frames = []
    for block in text.split("\n\n"):
        block = block.strip("\n")
        if not block:
            continue
        ev_name = None
        data_line = None
        for line in block.splitlines():
            if line.startswith("event:"):
                ev_name = line[len("event:"):].strip()
            elif line.startswith("data:"):
                data_line = line[len("data:"):].strip()
        frames.append({"event": ev_name, "data": json.loads(data_line) if data_line else None})
    return frames


def _install_fake_weixin(monkeypatch, qr_login):
    """往 sys.modules 注入假的 gateway.platforms.weixin，挂上给定 qr_login。

    channel_login 内 `from gateway.platforms.weixin import qr_login` 会命中此桩，
    从而无需真实 hermes SDK 即可走正常登录路径。
    """
    gateway = types.ModuleType("gateway")
    platforms = types.ModuleType("gateway.platforms")
    weixin = types.ModuleType("gateway.platforms.weixin")
    weixin.qr_login = qr_login
    monkeypatch.setitem(sys.modules, "gateway", gateway)
    monkeypatch.setitem(sys.modules, "gateway.platforms", platforms)
    monkeypatch.setitem(sys.modules, "gateway.platforms.weixin", weixin)


def test_login_sse_qrcode_then_bound(monkeypatch, ocops_schema):
    # 正常路径：qr_login 中途 print 二维码 URL 后返回 cred(truthy)
    # → SSE data 帧依次为 qrcode（url 与 print 行一致）与 bound
    import asyncio

    async def fake_qr_login(data_root, bot_type, timeout_seconds):
        # 模拟上游：先 print 二维码 URL（被 redirect 捕获成 qrcode 事件），
        # 让出事件循环让主循环先 yield qrcode，再返回凭证。
        print("https://liteapp.weixin.qq.com/q/abc?qrcode=tok&bot_type=3")
        await asyncio.sleep(0)
        return {"ok": 1}

    _install_fake_weixin(monkeypatch, fake_qr_login)
    c = _client(monkeypatch)
    r = c.post("/oc/channels/weixin/login", headers=_AUTH)
    assert r.status_code == 200
    assert r.headers["content-type"].startswith("text/event-stream")
    frames = _parse_sse(r.text)
    for frame in frames:
        validate(frame["data"], ocops_schema("channel/login-event.schema.json"))
    # 两帧 data：qrcode（含 url）与 bound，且无 event: 名（普通 data 帧）。
    assert frames == [
        {"event": None, "data": {"event": "qrcode",
                                 "url": "https://liteapp.weixin.qq.com/q/abc?qrcode=tok&bot_type=3"}},
        {"event": None, "data": {"event": "bound"}},
    ]


def test_login_sse_unknown_channel_failed(monkeypatch, ocops_schema):
    # 异常路径：未知 channel → 单条 failed data 帧（async generator 内部降级，不抛）
    c = _client(monkeypatch)
    r = c.post("/oc/channels/telegram/login", headers=_AUTH)
    assert r.status_code == 200
    frames = _parse_sse(r.text)
    validate(frames[0]["data"], ocops_schema("channel/login-event.schema.json"))
    assert frames == [{"event": None,
                       "data": {"event": "failed", "reason": "unknown channel: telegram"}}]


def test_watch_sse_two_events(monkeypatch, ocops_schema):
    # 正常路径：watch_events 同步 generator yield 两个 Event dict
    # → SSE 应收到两条对应 data 帧（线程池迭代不丢事件、保持顺序）
    from ocops import kanban

    def fake_watch_events(board):
        # 模拟两条契约事件；server 用 anyio.to_thread 在线程池逐条迭代。
        yield {"task_id": "t1", "kind": "task.created", "payload": {"board": board}, "created_at": 1, "run_id": None}
        yield {"task_id": "t1", "kind": "task.updated", "payload": {"board": board}, "created_at": 2, "run_id": None}

    monkeypatch.setattr(kanban, "watch_events", fake_watch_events)
    c = _client(monkeypatch)
    r = c.get("/oc/kanban/watch?board=default", headers=_AUTH)
    assert r.status_code == 200
    assert r.headers["content-type"].startswith("text/event-stream")
    frames = _parse_sse(r.text)
    for frame in frames:
        validate(frame["data"], ocops_schema("kanban/event.schema.json"))
    assert frames == [
        {"event": None, "data": {"task_id": "t1", "kind": "task.created",
                                  "payload": {"board": "default"}, "created_at": 1, "run_id": None}},
        {"event": None, "data": {"task_id": "t1", "kind": "task.updated",
                                  "payload": {"board": "default"}, "created_at": 2, "run_id": None}},
    ]


def test_watch_sse_kanban_error_emits_error_frame(monkeypatch, ocops_schema):
    # 异常路径：watch_events 启动即抛 KanbanError（OpsError 子类）
    # → server 应发 `event: error` 帧，data 含 code/message，而非中断连接
    from ocops import kanban
    from ocops.kanban import KanbanError

    def fake_watch_events(board):
        # 用 yield 让函数成为 generator，但迭代第一条即抛错（模拟启动失败）。
        raise KanbanError("UNSUPPORTED", "此镜像不含真实 hermes")
        yield  # noqa: unreachable - 使函数为 generator

    monkeypatch.setattr(kanban, "watch_events", fake_watch_events)
    c = _client(monkeypatch)
    r = c.get("/oc/kanban/watch?board=default", headers=_AUTH)
    assert r.status_code == 200
    frames = _parse_sse(r.text)
    # 单条 error 帧：event 名为 error，data 体携带契约 code 与 message。
    assert len(frames) == 1
    assert frames[0]["event"] == "error"
    validate(frames[0]["data"], ocops_schema("common/error.schema.json"))
    assert frames[0]["data"]["code"] == "UNSUPPORTED"
    assert frames[0]["data"]["message"] == "此镜像不含真实 hermes"


def test_watch_sse_requires_token(monkeypatch):
    # 鉴权：watch SSE 同样受 AuthMiddleware 保护，无 token → 401
    c = _client(monkeypatch)
    assert c.get("/oc/kanban/watch?board=default").status_code == 401
