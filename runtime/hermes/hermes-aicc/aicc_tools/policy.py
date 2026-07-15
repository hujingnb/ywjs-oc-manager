"""AICC 客服工具的不可变白名单与执行授权。

本模块只定义平台级上限，不读取企业配置。企业或 Skill 声明只能继续收紧该集合，
不能让外部访客客服获得终端、文件或任意网络写入能力。
"""

from __future__ import annotations

from collections.abc import Iterable, Mapping
from types import MappingProxyType
from typing import Any


# 镜像内置能力上限：与 manager 为 AICC 下发的 manifest capabilities 一一对应。
ALLOWED_CAPABILITIES = frozenset({"knowledge.read", "web.search", "skills.read", "vision.read"})

# capability 到 Hermes 具体工具名的固定映射。未映射的工具即使被上游注册，也不可对 AICC 公开。
CAPABILITY_TOOLS = MappingProxyType({
    "knowledge.read": frozenset({"aicc_knowledge_search"}),
    "web.search": frozenset({"web_search", "web_extract"}),
    "skills.read": frozenset({"skills_list", "skill_view"}),
    "vision.read": frozenset({"vision_analyze"}),
})

# clarify 不读取外部资源，但用于客服在信息不足时自然追问，属于固定安全工具。
ALLOWED_TOOLS = frozenset({"clarify"}) | frozenset().union(*CAPABILITY_TOOLS.values())


def allowed_tools_for_capabilities(capabilities: Iterable[str]) -> frozenset[str]:
    """返回 manifest 声明能力与镜像上限相交后的具体工具集合。

    未知 capability 由 manifest 解析阶段拒绝；这里仍只取交集，确保调用方错误传值时
    不会扩大权限。
    """
    enabled = frozenset(str(capability) for capability in capabilities) & ALLOWED_CAPABILITIES
    return frozenset({"clarify"}).union(*(CAPABILITY_TOOLS[capability] for capability in enabled))


def filter_definitions(items: Iterable[Mapping[str, Any]], capabilities: Iterable[str] | None = None) -> list[Mapping[str, Any]]:
    """仅保留模型可见的 AICC 工具定义，缺失函数名或非白名单定义一律丢弃。"""
    allowed = ALLOWED_TOOLS if capabilities is None else allowed_tools_for_capabilities(capabilities)
    filtered: list[Mapping[str, Any]] = []
    for item in items:
        function = item.get("function")
        name = function.get("name") if isinstance(function, Mapping) else None
        if isinstance(name, str) and name in allowed:
            filtered.append(item)
    return filtered


def authorize(name: str, capabilities: Iterable[str] | None = None) -> None:
    """在 dispatcher 进入 handler 前校验工具名，阻止模型伪造未公开的调用。"""
    allowed = ALLOWED_TOOLS if capabilities is None else allowed_tools_for_capabilities(capabilities)
    if name not in allowed:
        raise PermissionError(f"AICC_TOOL_FORBIDDEN: {name}")


def validate_manifest_capabilities(capabilities: Iterable[str]) -> frozenset[str]:
    """校验 manifest capability 只能来自客服镜像不可变上限。"""
    normalized = frozenset(str(capability) for capability in capabilities)
    unknown = normalized - ALLOWED_CAPABILITIES
    if unknown:
        raise ValueError(f"AICC_MANIFEST_CAPABILITY_FORBIDDEN: {', '.join(sorted(unknown))}")
    return normalized


def validate_skill_capabilities(declared: Iterable[str], manifest_capabilities: Iterable[str]) -> None:
    """校验客服 Skill 所声明能力是本次 manifest 授权能力的子集。"""
    declared_set = frozenset(str(capability) for capability in declared)
    allowed = validate_manifest_capabilities(manifest_capabilities)
    forbidden = declared_set - allowed
    if forbidden:
        raise ValueError(f"AICC_SKILL_CAPABILITY_FORBIDDEN: {', '.join(sorted(forbidden))}")
