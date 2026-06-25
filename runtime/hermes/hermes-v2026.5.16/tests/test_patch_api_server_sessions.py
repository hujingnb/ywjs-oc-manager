# tests/test_patch_api_server_sessions.py
"""验证 patch_api_server_sessions.py 补丁脚本的注入行为。

覆盖：
- 正常注入：在 HTTP Handlers 区块前插入会话块（含 _handle_list_sessions 等
  9 个 handler 与 _turn_transcript_messages），在路由区块末尾追加 9 条
  /api/sessions 路由注册行。
- 幂等性：重复执行不累积内容，返回相同对象引用（不触发写文件）。
- 锚点缺失：任一锚点不存在时抛 RuntimeError，阻止静默失败。
"""

import sys
from pathlib import Path

import pytest

# 将 patches/ 目录加入 sys.path，以便直接 import patch 模块
_PATCHES_DIR = Path(__file__).parent.parent / "patches"
sys.path.insert(0, str(_PATCHES_DIR))

import patch_api_server_sessions as _mod  # noqa: E402


# 最小仿真 api_server.py：含两个锚点（路由末尾行 + HTTP Handlers 区块）。
# 路由注册行 12 空格缩进，与 ROUTE_ANCHOR 一致。
_FAKE_API_SERVER = (
    "class APIServerAdapter:\n"
    "\n"
    "    async def connect(self):\n"
    '            self._app.router.add_post("/v1/runs/{run_id}/stop", self._handle_stop_run)\n'
    "            # other setup\n"
    "\n"
    "    # ------------------------------------------------------------------\n"
    "    # HTTP Handlers\n"
    "    # ------------------------------------------------------------------\n"
    "\n"
    "    async def _handle_health(self, request):\n"
    "        pass\n"
)


def test_injects_session_handlers_and_routes():
    # 正常注入：9 个 handler 名与 9 条路由路径都应出现
    result = _mod.patch(_FAKE_API_SERVER)
    for name in (
        "_handle_list_sessions",
        "_handle_create_session",
        "_handle_get_session",
        "_handle_patch_session",
        "_handle_delete_session",
        "_handle_session_messages",
        "_handle_fork_session",
        "_handle_session_chat",
        "_handle_session_chat_stream",
    ):
        assert name in result, f"应注入 handler {name}"
    assert result.count('add_get("/api/sessions"') == 1, "应注入 list 路由"
    assert '/api/sessions/{session_id}/chat/stream' in result, "应注入 chat/stream 路由"


def test_injects_turn_transcript_helper():
    # chat/stream 依赖的 classmethod helper 必须随块注入
    result = _mod.patch(_FAKE_API_SERVER)
    assert "_turn_transcript_messages" in result, "应注入 _turn_transcript_messages"


def test_handlers_before_http_handlers_section():
    # 会话块应插入到 HTTP Handlers 区块之前
    result = _mod.patch(_FAKE_API_SERVER)
    assert result.index("_handle_list_sessions") < result.index("# HTTP Handlers")


def test_routes_after_stop_run_route():
    # /api/sessions 路由应注册在 /v1/runs/{run_id}/stop 之后
    result = _mod.patch(_FAKE_API_SERVER)
    assert result.index('add_get("/api/sessions"') > result.index("/v1/runs/{run_id}/stop")


def test_idempotent():
    # 幂等：已注入则 early-return 相同对象，不重复累积
    first = _mod.patch(_FAKE_API_SERVER)
    second = _mod.patch(first)
    assert second is first
    assert first.count("async def _handle_list_sessions") == 1


def test_raises_if_handler_anchor_missing():
    # HTTP Handlers 锚点缺失 → RuntimeError
    bad = _FAKE_API_SERVER.replace("# HTTP Handlers\n", "# SomethingElse\n")
    with pytest.raises(RuntimeError, match="HTTP Handlers 锚点"):
        _mod.patch(bad)


def test_raises_if_route_anchor_missing():
    # 路由锚点缺失 → RuntimeError
    bad = _FAKE_API_SERVER.replace(
        'add_post("/v1/runs/{run_id}/stop", self._handle_stop_run)',
        'add_post("/v1/runs/{run_id}/stop", self._handle_other)',
    )
    with pytest.raises(RuntimeError, match="路由锚点"):
        _mod.patch(bad)
