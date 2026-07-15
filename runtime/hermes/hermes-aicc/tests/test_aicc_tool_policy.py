"""验证 AICC 客服镜像仅暴露经过能力策略批准的只读工具。"""

from __future__ import annotations

import importlib.util
import json
from pathlib import Path
from urllib.error import HTTPError

import pytest

from aicc_tools.aicc_knowledge_tool import search_knowledge
from aicc_tools.policy import (
    ALLOWED_CAPABILITIES,
    ALLOWED_TOOLS,
    authorize,
    filter_definitions,
    validate_skill_capabilities,
)
from lib.manifest import ManifestError, load


# 覆盖：客服镜像暴露的工具定义只能来自平台不可变白名单，通用执行入口不得被模型发现。
def test_policy_filters_definitions_and_rejects_every_non_allowlisted_tool() -> None:
    definitions = [
        {"function": {"name": name}}
        for name in sorted(ALLOWED_TOOLS | {"terminal", "skill_manage"})
    ]

    assert authorize("web_search") is None
    assert [item["function"]["name"] for item in filter_definitions(definitions)] == sorted(ALLOWED_TOOLS)
    for name in [
        "terminal",
        "process",
        "execute_code",
        "read_file",
        "write_file",
        "skill_manage",
        "browser_click",
        "cronjob",
    ]:
        with pytest.raises(PermissionError, match="AICC_TOOL_FORBIDDEN"):
            authorize(name)


# 覆盖：Skill 声明的能力只能是本轮 AICC manifest 下发能力的子集，越权声明必须在启动期拒绝。
def test_policy_rejects_skill_capabilities_outside_manifest_subset() -> None:
    assert validate_skill_capabilities(["knowledge.read"], ["knowledge.read", "web.search"]) is None

    with pytest.raises(ValueError, match="AICC_SKILL_CAPABILITY_FORBIDDEN"):
        validate_skill_capabilities(["knowledge.write"], ALLOWED_CAPABILITIES)


# 覆盖：AICC manifest 只能接受平台定义的 capability，未知能力不能因 forward-compat 被静默放行。
def test_manifest_rejects_unknown_aicc_capability(tmp_path: Path) -> None:
    manifest = tmp_path / "manifest.yaml"
    manifest.write_text(
        """
app: {id: app-x, name: x, model: m}
credentials: {openai: {api_key: sk-x, base_url: http://new-api}}
resources: {persona: resources/persona.md, rules: {platform: resources/platform-rules.md}}
capabilities: [knowledge.read, terminal.execute]
""",
        encoding="utf-8",
    )

    with pytest.raises(ManifestError, match="unknown AICC capability"):
        load(manifest)


# 覆盖：知识工具只向 manager runtime API 发送固定 top_k 的问题，不给模型传入数据集、URL 或写入参数。
def test_knowledge_search_posts_only_question_and_fixed_top_k(monkeypatch: pytest.MonkeyPatch) -> None:
    captured: dict[str, object] = {}

    class Response:
        # 模拟 manager 返回的 UTF-8 JSON 响应。
        def read(self) -> bytes:
            return b'{"results":[{"content":"answer"}]}'

        def __enter__(self) -> "Response":
            return self

        def __exit__(self, *_args: object) -> None:
            return None

    def fake_urlopen(request: object, timeout: float) -> Response:
        captured["url"] = request.full_url
        captured["method"] = request.method
        captured["headers"] = dict(request.header_items())
        captured["body"] = json.loads(request.data.decode("utf-8"))
        captured["timeout"] = timeout
        return Response()

    monkeypatch.setenv("OC_KB_RUNTIME_BASE_URL", "http://manager-runtime/")
    monkeypatch.setenv("OC_KB_APP_TOKEN", "app-scoped-token")
    monkeypatch.setattr("aicc_tools.aicc_knowledge_tool.urlopen", fake_urlopen)

    assert search_knowledge("套餐价格") == {"results": [{"content": "answer"}]}
    assert captured["url"] == "http://manager-runtime/api/v1/runtime/knowledge/search"
    assert captured["method"] == "POST"
    assert captured["body"] == {"question": "套餐价格", "top_k": 8}
    assert captured["headers"]["Authorization"] == "Bearer app-scoped-token"


# 覆盖：manager runtime API 拒绝时，知识工具必须保留失败语义，不得把错误伪装成空知识结果。
def test_knowledge_search_raises_runtime_error_for_manager_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    def failing_urlopen(_request: object, *, timeout: float) -> object:
        raise HTTPError("http://manager", 503, "unavailable", {}, None)

    monkeypatch.setenv("OC_KB_RUNTIME_BASE_URL", "http://manager-runtime")
    monkeypatch.setenv("OC_KB_APP_TOKEN", "app-scoped-token")
    monkeypatch.setattr("aicc_tools.aicc_knowledge_tool.urlopen", failing_urlopen)

    with pytest.raises(RuntimeError, match="AICC_KNOWLEDGE_SEARCH_FAILED"):
        search_knowledge("售后政策")


# 覆盖：构建期补丁对上游两个插入点各替换一次，缺锚点或重复锚点时必须失败而不能静默生成半保护镜像。
def test_patch_requires_exactly_one_anchor_for_each_injection() -> None:
    patch_path = Path(__file__).resolve().parents[1] / "patches" / "patch_aicc_tool_policy.py"
    spec = importlib.util.spec_from_file_location("patch_aicc_tool_policy", patch_path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)

    source = (
        module.IMPORT_ANCHOR
        + module.DISCOVERY_ANCHOR
        + module.DEFINITIONS_ANCHOR
        + module.DISPATCH_ANCHOR
    )
    patched = module.patch(source)
    assert patched.count("filtered_tools = filter_aicc_tool_definitions(filtered_tools)") == 1
    assert patched.count("authorize_aicc_tool") == 2  # import 与 dispatcher guard 各一处。

    with pytest.raises(RuntimeError, match="expected exactly one"):
        module.patch(
            module.IMPORT_ANCHOR
            + module.DISCOVERY_ANCHOR
            + module.DEFINITIONS_ANCHOR
            + module.DEFINITIONS_ANCHOR
            + module.DISPATCH_ANCHOR
        )
