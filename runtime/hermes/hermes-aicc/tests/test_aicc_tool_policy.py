"""验证 AICC 客服镜像仅暴露经过能力策略批准的只读工具。"""

from __future__ import annotations

import importlib.util
import json
from pathlib import Path
import py_compile
from urllib.error import HTTPError

import pytest

from aicc_tools.aicc_knowledge_tool import search_knowledge
from aicc_tools.aicc_web_audit_tools import append_web_audit
from aicc_tools.policy import (
    ALLOWED_CAPABILITIES,
    ALLOWED_TOOLS,
    authorize,
    current_manifest_capabilities,
    filter_definitions,
    validate_skill_capabilities,
    require_manifest_capabilities,
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
        "tool_search",
        "tool_describe",
        "tool_call",
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


# 覆盖：AICC 容器启动必须拿到完整只读能力集合，缺失 manifest capabilities 时不能退化为全权限。
def test_policy_rejects_missing_required_manifest_capabilities() -> None:
    with pytest.raises(ValueError, match="AICC_MANIFEST_CAPABILITY_MISSING"):
        require_manifest_capabilities(["knowledge.read"])


# 覆盖：definition filter 与 dispatcher 共用 entrypoint 下发的 manifest 能力，环境缺失时必须失败关闭。
def test_policy_reads_manifest_capabilities_from_runtime_environment(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("OC_AICC_CAPABILITIES", "knowledge.read,web.search,skills.read,vision.read")
    assert current_manifest_capabilities() == ALLOWED_CAPABILITIES

    monkeypatch.delenv("OC_AICC_CAPABILITIES")
    with pytest.raises(ValueError, match="AICC_MANIFEST_CAPABILITY_MISSING"):
        current_manifest_capabilities()


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

    assert json.loads(search_knowledge({"question": "套餐价格"})) == {"results": [{"content": "answer"}], "aicc_response_sources": []}
    assert captured["url"] == "http://manager-runtime/api/v1/runtime/knowledge/search"
    assert captured["method"] == "POST"
    assert captured["body"] == {"question": "套餐价格", "top_k": 8}
    assert captured["headers"]["Authorization"] == "Bearer app-scoped-token"
    assert captured["headers"]["X-oc-app-token"] == "app-scoped-token"


def test_knowledge_search_ignores_hermes_task_metadata(monkeypatch: pytest.MonkeyPatch) -> None:
    """Hermes registry 会注入任务元数据，客服检索工具不能因只读追踪字段而拒绝访客问题。"""
    class Response:
        def read(self) -> bytes:
            return b'{"results": []}'

        def __enter__(self) -> "Response":
            return self

        def __exit__(self, *_args: object) -> None:
            return None

    monkeypatch.setenv("OC_KB_RUNTIME_BASE_URL", "http://manager-runtime")
    monkeypatch.setenv("OC_KB_APP_TOKEN", "app-scoped-token")
    monkeypatch.setattr("aicc_tools.aicc_knowledge_tool.urlopen", lambda *_args, **_kwargs: Response())

    assert json.loads(search_knowledge({"question": "套餐价格"}, task_id="task-1")) == {"results": [], "aicc_response_sources": []}


# 覆盖：知识工具必须把实际检索命中变成稳定来源引用，供 manager 从本轮 tool transcript 重建白名单。
def test_knowledge_search_returns_auditable_response_sources(monkeypatch: pytest.MonkeyPatch) -> None:
    class Response:
        def read(self) -> bytes:
            return '{"results":[{"scope":"app","document_id":"doc-1","document_name":"企业手册"}]}'.encode("utf-8")

        def __enter__(self) -> "Response":
            return self

        def __exit__(self, *_args: object) -> None:
            return None

    monkeypatch.setenv("OC_KB_RUNTIME_BASE_URL", "http://manager-runtime")
    monkeypatch.setenv("OC_KB_APP_TOKEN", "app-scoped-token")
    monkeypatch.setattr("aicc_tools.aicc_knowledge_tool.urlopen", lambda *_args, **_kwargs: Response())

    result = json.loads(search_knowledge({"question": "企业版功能"}))

    assert result["aicc_response_sources"] == [
        {"type": "knowledge", "title": "企业手册", "scope": "app", "reference_id": "knowledge:app:doc-1:0"}
    ]


# 覆盖：网页检索和网页提取都会返回稳定来源；企业域名命中时必须标识为未经企业确认。
def test_web_tools_return_auditable_sources_with_enterprise_network_flag(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("OC_AICC_ENTERPRISE_DOMAINS", "example.com")

    result = json.loads(append_web_audit(json.dumps({"results": [
        {"title": "企业官网", "url": "https://www.example.com/product"},
        {"title": "第三方报道", "url": "https://news.example.net/story"},
    ]})))

    assert result["aicc_response_sources"][0]["scope"] == "enterprise_network"
    assert result["aicc_response_sources"][0]["unconfirmed"] is True
    assert result["aicc_response_sources"][0]["reference_id"].startswith("web:enterprise_network:0:")
    assert result["aicc_response_sources"][1]["scope"] == "public_network"
    assert result["aicc_response_sources"][1]["unconfirmed"] is False


# 覆盖：manager runtime API 拒绝时，知识工具必须保留失败语义，不得把错误伪装成空知识结果。
def test_knowledge_search_raises_runtime_error_for_manager_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    def failing_urlopen(_request: object, *, timeout: float) -> object:
        raise HTTPError("http://manager", 503, "unavailable", {}, None)

    monkeypatch.setenv("OC_KB_RUNTIME_BASE_URL", "http://manager-runtime")
    monkeypatch.setenv("OC_KB_APP_TOKEN", "app-scoped-token")
    monkeypatch.setattr("aicc_tools.aicc_knowledge_tool.urlopen", failing_urlopen)

    with pytest.raises(RuntimeError, match="AICC_KNOWLEDGE_SEARCH_FAILED"):
        search_knowledge({"question": "售后政策"})


# 覆盖：构建期补丁对上游五个插入点各替换一次，且补丁结果可编译、dispatcher 守卫先于桥接工具分支。
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
        + module.ASSEMBLY_ANCHOR
        + module.DISPATCH_ANCHOR
    )
    patched = module.patch(source)
    assert patched.count("filtered_tools = filter_aicc_tool_definitions(filtered_tools, current_manifest_capabilities())") == 2
    assert patched.count("authorize_aicc_tool") == 2  # import 与 dispatcher guard 各一处。

    with pytest.raises(RuntimeError, match="expected exactly one"):
        module.patch(
            module.IMPORT_ANCHOR
            + module.DISCOVERY_ANCHOR
            + module.DEFINITIONS_ANCHOR
            + module.DEFINITIONS_ANCHOR
            + module.ASSEMBLY_ANCHOR
            + module.DISPATCH_ANCHOR
        )


# 覆盖：真实 dispatcher 的桥接工具分支之前先做授权；伪造 tool_search/tool_describe/tool_call 不得绕过守卫。
def test_patch_places_guard_before_tool_search_bridge_and_compiles(tmp_path: Path) -> None:
    patch_path = Path(__file__).resolve().parents[1] / "patches" / "patch_aicc_tool_policy.py"
    spec = importlib.util.spec_from_file_location("patch_aicc_tool_policy", patch_path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)

    source = """from tools.registry import discover_builtin_tools, registry
discover_builtin_tools()
def get_tools():
    filtered_tools = registry.get_definitions(tools_to_include, quiet=quiet_mode)
    if enabled:
            filtered_tools = assembly.tool_defs
    return filtered_tools
def handle(function_name):
    function_args = {}
    _tool_middleware_trace = list(tool_request_middleware_trace or [])
    # ── Tool Search bridge dispatch ──────────────────────────────────
    if function_name in {\"tool_search\", \"tool_describe\", \"tool_call\"}:
        return \"bridge\"
    try:
        if function_name in _AGENT_LOOP_TOOLS:
            return \"agent\"
    except Exception:
        return \"error\"
"""
    patched = module.patch(source)
    result = tmp_path / "model_tools.py"
    result.write_text(patched, encoding="utf-8")
    py_compile.compile(str(result), doraise=True)
    assert patched.index("authorize_aicc_tool(function_name, current_manifest_capabilities())") < patched.index("Tool Search bridge dispatch")
    assert patched.index("authorize_aicc_tool(function_name, current_manifest_capabilities())") < patched.index("tool_call")


# 覆盖：Tool Search assembly 重新加入 bridge 工具后，最终返回模型的定义仍严格落在 AICC 白名单内。
def test_patch_filters_bridge_definitions_after_tool_search_assembly() -> None:
    patch_path = Path(__file__).resolve().parents[1] / "patches" / "patch_aicc_tool_policy.py"
    spec = importlib.util.spec_from_file_location("patch_aicc_tool_policy", patch_path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)

    source = (
        module.IMPORT_ANCHOR
        + module.DISCOVERY_ANCHOR
        + module.DEFINITIONS_ANCHOR
        + module.ASSEMBLY_ANCHOR
        + "    return filtered_tools\n"
        + module.DISPATCH_ANCHOR
    )
    patched = module.patch(source)
    assert patched.index("filtered_tools = filter_aicc_tool_definitions(filtered_tools, current_manifest_capabilities())", patched.index(module.ASSEMBLY_ANCHOR)) > patched.index(module.ASSEMBLY_ANCHOR)

    assembled = [
        {"function": {"name": "aicc_knowledge_search"}},
        {"function": {"name": "tool_search"}},
        {"function": {"name": "tool_describe"}},
        {"function": {"name": "tool_call"}},
    ]
    assert [item["function"]["name"] for item in filter_definitions(assembled)] == ["aicc_knowledge_search"]
