# tests/test_patch_api_server_reload.py
"""验证 patch_api_server_reload.py 补丁脚本的注入行为。

覆盖：
- 正常注入：在 handler 区块前插入 _handle_oc_skills_reload 方法，
  在路由区块末尾追加 /oc/skills/reload 路由注册行。
- 幂等性：重复执行不累积内容，返回相同对象引用（不触发写文件）。
- 锚点缺失：任一锚点不存在时抛 RuntimeError，阻止静默失败。
"""

import sys
from pathlib import Path

import pytest

# 将 patches/ 目录加入 sys.path，以便直接 import patch 模块
_PATCHES_DIR = Path(__file__).parent.parent / "patches"
sys.path.insert(0, str(_PATCHES_DIR))

import patch_api_server_reload as _mod  # noqa: E402


# ---------------------------------------------------------------------------
# 最小仿真 api_server.py 内容：包含两个锚点（handler 区块 + 路由末尾行）。
# 注意缩进层级：真实文件 connect() 在类体内的深层嵌套结构中，路由注册行为
# 12 个空格缩进，与 ROUTE_ANCHOR 中定义的锚点字符串保持一致。
# ---------------------------------------------------------------------------
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


def test_patch_injects_handler_and_route():
    # 正常注入：handler 方法和路由注册行都应出现在输出中
    result = _mod.patch(_FAKE_API_SERVER)
    assert "_handle_oc_skills_reload" in result, "应注入 handler 方法名"
    assert "/oc/skills/reload" in result, "应注入路由路径 /oc/skills/reload"
    assert "reload_skills" in result, "handler 应调用 reload_skills()"


def test_patch_handler_before_http_handlers_section():
    # 方法应插入到 HTTP Handlers 区块之前，保持文件结构清晰
    result = _mod.patch(_FAKE_API_SERVER)
    handler_idx = result.index("_handle_oc_skills_reload")
    http_handlers_idx = result.index("# HTTP Handlers")
    assert handler_idx < http_handlers_idx, (
        "_handle_oc_skills_reload 方法应在 HTTP Handlers 区块之前"
    )


def test_patch_route_after_stop_run_route():
    # /oc/skills/reload 路由应注册在 /v1/runs/{run_id}/stop 路由之后
    result = _mod.patch(_FAKE_API_SERVER)
    stop_run_idx = result.index("/v1/runs/{run_id}/stop")
    reload_route_idx = result.index("/oc/skills/reload")
    assert reload_route_idx > stop_run_idx, (
        "/oc/skills/reload 路由注册行应在 /v1/runs/{run_id}/stop 之后"
    )


def test_patch_idempotent():
    # 幂等性：重复调用返回相同对象引用（patch() 内已注入时 early-return），
    # 保证 Dockerfile RUN 重复执行不累积内容
    first = _mod.patch(_FAKE_API_SERVER)
    second = _mod.patch(first)
    assert second is first, "已注入后 patch() 应返回相同对象（幂等）"
    assert first.count("_handle_oc_skills_reload") == second.count(
        "_handle_oc_skills_reload"
    ), "幂等调用不应重复注入 handler"


def test_patch_raises_if_handler_anchor_missing():
    # 锚点缺失（HTTP Handlers 区块不存在）→ RuntimeError，阻止静默失败
    content_no_handler_anchor = _FAKE_API_SERVER.replace(
        "# HTTP Handlers\n", "# SomethingElse\n"
    )
    with pytest.raises(RuntimeError, match="HTTP Handlers 锚点"):
        _mod.patch(content_no_handler_anchor)


def test_patch_raises_if_route_anchor_missing():
    # 锚点缺失（路由末尾行不存在）→ RuntimeError，阻止静默失败
    content_no_route_anchor = _FAKE_API_SERVER.replace(
        'self._app.router.add_post("/v1/runs/{run_id}/stop", self._handle_stop_run)',
        'self._app.router.add_post("/v1/runs/{run_id}/stop", self._handle_other)',
    )
    with pytest.raises(RuntimeError, match="路由锚点"):
        _mod.patch(content_no_route_anchor)
