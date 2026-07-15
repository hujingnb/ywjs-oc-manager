#!/usr/bin/env python3
"""构建期把 AICC 不可变工具策略接入 Hermes 的定义与 dispatcher 两条路径。

该补丁只用于 hermes-aicc 专用镜像。任一稳定锚点发生上游漂移、重复或已经注入时都
直接失败，避免构建出模型可见工具与实际执行守卫不一致的半保护镜像。
"""

from __future__ import annotations

from pathlib import Path


TARGET = Path("/usr/local/lib/hermes-agent/model_tools.py")

# 上游 v2026.7.1 的精确稳定文本，分别覆盖 import、工具发现、定义生成与执行分发。
IMPORT_ANCHOR = "from tools.registry import discover_builtin_tools, registry\n"
DISCOVERY_ANCHOR = "discover_builtin_tools()\n"
DEFINITIONS_ANCHOR = "    filtered_tools = registry.get_definitions(tools_to_include, quiet=quiet_mode)\n"
DISPATCH_ANCHOR = "    _tool_middleware_trace = list(tool_request_middleware_trace or [])\n"

IMPORT_INJECT = (
    "from aicc_tools.policy import authorize as authorize_aicc_tool, "
    "current_manifest_capabilities, filter_definitions as filter_aicc_tool_definitions\n"
    "from aicc_tools.aicc_knowledge_tool import register_with_hermes_registry\n"
)
DISCOVERY_INJECT = "register_with_hermes_registry(registry)\n"
DEFINITIONS_INJECT = "    filtered_tools = filter_aicc_tool_definitions(filtered_tools, current_manifest_capabilities())\n"
DISPATCH_INJECT = "    authorize_aicc_tool(function_name, current_manifest_capabilities())\n"


def _inject_once(content: str, anchor: str, injected: str, label: str) -> str:
    """仅替换一次精确锚点；数量异常即中断镜像构建。"""
    count = content.count(anchor)
    if count != 1:
        raise RuntimeError(f"patch_aicc_tool_policy: {label} expected exactly one anchor, found {count}")
    if injected in content:
        raise RuntimeError(f"patch_aicc_tool_policy: {label} expected exactly one injection, found existing marker")
    return content.replace(anchor, anchor + injected, 1)


def patch(content: str) -> str:
    """为给定的上游 model_tools.py 文本注入四个不可缺少的 AICC 防线。"""
    content = _inject_once(content, IMPORT_ANCHOR, IMPORT_INJECT, "import")
    content = _inject_once(content, DISCOVERY_ANCHOR, DISCOVERY_INJECT, "discovery")
    content = _inject_once(content, DEFINITIONS_ANCHOR, DEFINITIONS_INJECT, "definitions")
    return _inject_once(content, DISPATCH_ANCHOR, DISPATCH_INJECT, "dispatcher")


if __name__ == "__main__":
    original = TARGET.read_text(encoding="utf-8")
    TARGET.write_text(patch(original), encoding="utf-8")
