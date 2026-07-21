# tests/test_server_conversation.py
"""覆盖 oc-ops server 的会话路由：鉴权透传、list/messages/create/delete/chat 落到
conversation 模块、OpsError→HTTP 状态码映射正确。

auth 模式与既有 server kanban/skills 测试一致：
  monkeypatch.setenv("OC_OPS_TOKEN", "t0ken") + Authorization: Bearer t0ken。
"""
from __future__ import annotations

import json
from pathlib import Path
from unittest import mock

import pytest
from starlette.testclient import TestClient

from ocops.errors import OpsError


# ---------------------------------------------------------------------------
# 公共辅助
# ---------------------------------------------------------------------------

def _client(monkeypatch, tmp_path: Path) -> TestClient:
    """构造带固定 token 的测试 client。

    OC_OPS_TOKEN 固定为 t0ken，OC_INFO_FILE 指向 tmp_path 下假文件，
    与既有 server 测试（kanban/skills）保持完全相同的初始化手法。
    """
    monkeypatch.setenv("OC_OPS_TOKEN", "t0ken")
    info_file = tmp_path / "oc-image.json"
    info_file.write_text(json.dumps({
        "variant": "hermes-v0.19.0",
        "hermes_upstream_ref": "abc",
        "built_at": "2026-06-23",
    }))
    monkeypatch.setenv("OC_INFO_FILE", str(info_file))
    from ocops.server import app
    return TestClient(app)


def _auth() -> dict:
    """返回带正确 Bearer token 的请求头。"""
    return {"Authorization": "Bearer t0ken"}


# ---------------------------------------------------------------------------
# GET /oc/conversations：列出会话，透传 source/limit/offset 查询参数。
# ---------------------------------------------------------------------------

def test_list_route(monkeypatch, tmp_path):
    # 正常路径：list_sessions 返回列表，HTTP 200，source 参数透传。
    with mock.patch("ocops.server.conversation.list_sessions", return_value=[{"id": "s1"}]) as m:
        r = _client(monkeypatch, tmp_path).get(
            "/oc/conversations?source=weixin&limit=10", headers=_auth()
        )
    assert r.status_code == 200
    assert r.json() == [{"id": "s1"}]
    # 校验 source 关键字参数已透传
    assert m.call_args.kwargs.get("source") == "weixin"


def test_list_limit_offset(monkeypatch, tmp_path):
    # 边界：limit/offset 均透传为整数，offset 默认 0。
    with mock.patch("ocops.server.conversation.list_sessions", return_value=[]) as m:
        r = _client(monkeypatch, tmp_path).get(
            "/oc/conversations?limit=5&offset=20", headers=_auth()
        )
    assert r.status_code == 200
    assert m.call_args.kwargs.get("limit") == 5
    assert m.call_args.kwargs.get("offset") == 20


# ---------------------------------------------------------------------------
# GET /oc/conversations/{sid}/messages：读历史，NOT_FOUND → 404。
# ---------------------------------------------------------------------------

def test_messages_notfound(monkeypatch, tmp_path):
    # 异常路径：会话不存在，OpsError(NOT_FOUND) 映射为 404。
    with mock.patch(
        "ocops.server.conversation.session_messages",
        side_effect=OpsError("NOT_FOUND", "会话不存在"),
    ):
        r = _client(monkeypatch, tmp_path).get(
            "/oc/conversations/nope/messages", headers=_auth()
        )
    assert r.status_code == 404
    assert r.json()["code"] == "NOT_FOUND"


def test_messages_ok(monkeypatch, tmp_path):
    # 正常路径：返回消息列表，HTTP 200，sid 透传到 session_messages。
    with mock.patch(
        "ocops.server.conversation.session_messages",
        return_value=[{"role": "user", "content": "hello"}],
    ) as m:
        r = _client(monkeypatch, tmp_path).get(
            "/oc/conversations/sid123/messages", headers=_auth()
        )
    assert r.status_code == 200
    assert r.json()[0]["role"] == "user"
    assert m.call_args[0][0] == "sid123"


# ---------------------------------------------------------------------------
# POST /oc/conversations：新建会话，返回 201。
# ---------------------------------------------------------------------------

def test_create_route(monkeypatch, tmp_path):
    # 正常路径：create_session 返回新会话对象，HTTP 201。
    new_session = {"id": "new1", "source": "weixin"}
    with mock.patch("ocops.server.conversation.create_session", return_value=new_session) as m:
        r = _client(monkeypatch, tmp_path).post(
            "/oc/conversations", headers=_auth(), json={"source": "weixin", "title": "test"}
        )
    assert r.status_code == 201
    assert r.json()["id"] == "new1"
    # body 整体透传到 create_session
    assert m.call_args[0][0]["source"] == "weixin"


# ---------------------------------------------------------------------------
# DELETE /oc/conversations/{sid}：删除会话，返回 204 无 body。
# ---------------------------------------------------------------------------

def test_delete_route(monkeypatch, tmp_path):
    # 正常路径：delete_session 无返回，HTTP 204。
    with mock.patch("ocops.server.conversation.delete_session") as m:
        r = _client(monkeypatch, tmp_path).delete(
            "/oc/conversations/sid-del", headers=_auth()
        )
    assert r.status_code == 204
    assert m.call_args[0][0] == "sid-del"


def test_delete_notfound(monkeypatch, tmp_path):
    # 异常路径：会话不存在，OpsError(NOT_FOUND) → 404。
    with mock.patch(
        "ocops.server.conversation.delete_session",
        side_effect=OpsError("NOT_FOUND", "x"),
    ):
        r = _client(monkeypatch, tmp_path).delete(
            "/oc/conversations/gone", headers=_auth()
        )
    assert r.status_code == 404


# ---------------------------------------------------------------------------
# POST /oc/conversations/{sid}/chat：单轮续聊，透传 body，返回 assistant 消息。
# ---------------------------------------------------------------------------

def test_chat_route(monkeypatch, tmp_path):
    # 正常路径：chat 返回 assistant 回复对象，HTTP 200，sid 与 body 均透传。
    with mock.patch(
        "ocops.server.conversation.chat",
        return_value={"message": {"content": "ok"}},
    ) as m:
        r = _client(monkeypatch, tmp_path).post(
            "/oc/conversations/s1/chat", headers=_auth(), json={"message": "hi"}
        )
    assert r.status_code == 200
    assert r.json()["message"]["content"] == "ok"
    # 第一个位置参数是 sid
    assert m.call_args[0][0] == "s1"
    # body 透传，message 字段正确
    assert m.call_args[0][1]["message"] == "hi"


def test_chat_internal_error(monkeypatch, tmp_path):
    # 异常路径：api_server 内部错误，OpsError(INTERNAL) → 500。
    with mock.patch(
        "ocops.server.conversation.chat",
        side_effect=OpsError("INTERNAL", "api_server 连接失败"),
    ):
        r = _client(monkeypatch, tmp_path).post(
            "/oc/conversations/s1/chat", headers=_auth(), json={"message": "hi"}
        )
    assert r.status_code == 500
    assert r.json()["code"] == "INTERNAL"


# 重命名：PATCH /oc/conversations/{sid} 透传 title 给 conversation.update_title。
def test_rename_route(monkeypatch, tmp_path):
    with mock.patch("ocops.server.conversation.update_title", return_value={"id": "s1", "title": "新名"}) as m:
        r = _client(monkeypatch, tmp_path).patch("/oc/conversations/s1", headers=_auth(), json={"title": "新名"})
    assert r.status_code == 200 and r.json()["title"] == "新名"
    assert m.call_args[0] == ("s1", "新名")


# 流式续聊：server 把 conversation.chat_stream 的逐帧 bytes 透传为 text/event-stream。
def test_chat_stream_route(monkeypatch, tmp_path):
    def fake_stream(sid, body):
        yield b'data: {"event":"assistant.delta","payload":{"delta":"he"}}\n\n'
        yield b'data: {"event":"assistant.completed","payload":{}}\n\n'
    with mock.patch("ocops.server.conversation.chat_stream", side_effect=fake_stream):
        r = _client(monkeypatch, tmp_path).post(
            "/oc/conversations/s1/chat/stream", headers=_auth(), json={"message": "hi"}
        )
    assert r.status_code == 200
    assert "assistant.delta" in r.text and "assistant.completed" in r.text


# 流式续聊出错：上游抛 OpsError 时，server 必须把错误规整为「正常 data 帧」
# {event:error,payload:{code,message}}，而非 SSE 命名事件 `event: error`——否则下游
# Go scanSSE 会吞掉 data、前端只看到空回复（code review #1）。
def test_chat_stream_error_emitted_as_data_frame(monkeypatch, tmp_path):
    def boom(sid, body):
        raise OpsError("INTERNAL", "上游炸了")
        yield  # noqa: 使其成为生成器（raise 在 yield 前触发）
    with mock.patch("ocops.server.conversation.chat_stream", side_effect=boom):
        r = _client(monkeypatch, tmp_path).post(
            "/oc/conversations/s1/chat/stream", headers=_auth(), json={"message": "hi"}
        )
    assert r.status_code == 200
    # 错误以 data 帧承载、含 event:error 与错误码，不出现裸 `event: error` 命名事件行。
    # 注：json.dumps 默认 ensure_ascii，中文 message 被转义为 \uXXXX，故断言用 ASCII 的 code。
    assert '"event": "error"' in r.text and '"code": "INTERNAL"' in r.text
    assert "event: error" not in r.text
