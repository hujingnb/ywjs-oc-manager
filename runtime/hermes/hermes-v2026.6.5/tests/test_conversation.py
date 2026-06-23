# 覆盖 conversation 模块对 api_server 的转发：鉴权头注入、非 2xx → OpsError、列表/历史/新建/删除 path 拼装。
import json
import urllib.error
from unittest import mock

import pytest

from ocops import conversation
from ocops.errors import OpsError


class _FakeResp:
    """模拟 urllib urlopen 的上下文管理返回体。"""
    def __init__(self, body: bytes):
        self._body = body
    def __enter__(self):
        return self
    def __exit__(self, *a):
        return False
    def read(self):
        return self._body


# 列会话：带 source/limit query，注入 Bearer，透传 api_server JSON 的 data 数组。
def test_list_sessions_forwards_and_unwraps():
    payload = json.dumps({"object": "list", "data": [{"id": "s1", "source": "weixin"}]}).encode()
    with mock.patch.object(conversation, "_api_server_key", return_value="k"), \
         mock.patch("urllib.request.urlopen", return_value=_FakeResp(payload)) as op:
        out = conversation.list_sessions(source="weixin", limit=50, offset=0)
    assert out == [{"id": "s1", "source": "weixin"}]
    req = op.call_args[0][0]
    assert req.get_header("Authorization") == "Bearer k"
    assert "source=weixin" in req.full_url and "limit=50" in req.full_url


# 读历史：path 含 session id（转义），返回 api_server messages 数组。
def test_session_messages_path_and_passthrough():
    payload = json.dumps({"data": [{"role": "user", "content": "hi"}]}).encode()
    with mock.patch.object(conversation, "_api_server_key", return_value="k"), \
         mock.patch("urllib.request.urlopen", return_value=_FakeResp(payload)) as op:
        out = conversation.session_messages("s 1")
    assert out == [{"role": "user", "content": "hi"}]
    assert "/api/sessions/s%201/messages" in op.call_args[0][0].full_url


# api_server 返回 404 → 抛 OpsError("NOT_FOUND")，供 server 映射 404。
def test_non_2xx_maps_to_opserror():
    err = urllib.error.HTTPError("u", 404, "nf", {}, None)
    with mock.patch.object(conversation, "_api_server_key", return_value="k"), \
         mock.patch("urllib.request.urlopen", side_effect=err):
        with pytest.raises(OpsError) as ei:
            conversation.session_messages("nope")
    assert ei.value.code == "NOT_FOUND"


# 续聊：POST /chat，body 透传 message，返回 assistant 回复对象。
def test_chat_posts_message():
    payload = json.dumps({"session_id": "s1", "message": {"role": "assistant", "content": "ok"}}).encode()
    with mock.patch.object(conversation, "_api_server_key", return_value="k"), \
         mock.patch("urllib.request.urlopen", return_value=_FakeResp(payload)) as op:
        out = conversation.chat("s1", {"message": "hi"})
    assert out["message"]["content"] == "ok"
    assert op.call_args[0][0].method == "POST"


# 新建会话：api_server 返回 {"object","session":{...}} 包裹形状，create_session 必须
# 解包 session 键返回扁平会话对象，否则下游 manager 解不出 id（真机验证发现的 bug）。
def test_create_session_unwraps_session_key():
    payload = json.dumps({"object": "hermes.session", "session": {"id": "api_1", "source": "api_server"}}).encode()
    with mock.patch.object(conversation, "_api_server_key", return_value="k"), \
         mock.patch("urllib.request.urlopen", return_value=_FakeResp(payload)) as op:
        out = conversation.create_session({"source": "web"})
    assert out == {"id": "api_1", "source": "api_server"}  # 已解包到扁平对象
    assert op.call_args[0][0].method == "POST"


# 重命名：PATCH 同样返回 {"object","session":{...}}，update_title 必须解包 session 键。
def test_update_title_unwraps_session_key():
    payload = json.dumps({"object": "hermes.session", "session": {"id": "api_1", "title": "新名"}}).encode()
    with mock.patch.object(conversation, "_api_server_key", return_value="k"), \
         mock.patch("urllib.request.urlopen", return_value=_FakeResp(payload)) as op:
        out = conversation.update_title("api_1", "新名")
    assert out == {"id": "api_1", "title": "新名"}  # 已解包
    assert op.call_args[0][0].method == "PATCH"
