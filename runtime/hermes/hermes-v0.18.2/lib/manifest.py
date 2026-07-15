"""manager 写入的 manifest.yaml 解析与必填字段校验。

仅暴露 forward-compat 的字段视图：调用方拿到 Manifest 实例后访问命名属性，
未知字段被忽略；缺必填字段则抛 ManifestError，让 oc-entrypoint 直接退出 1。
"""

from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Union

import yaml


class ManifestError(Exception):
    """manifest 缺失必填字段或解析失败。"""


@dataclass(frozen=True)
class Manifest:
    """业务化视图。字段语义对应 spec §4.2 / §5（manifest v2）。"""
    app_id: str
    app_name: str
    app_model: str
    openai_api_key: str
    openai_base_url: str
    persona_rel: str
    rule_platform_rel: str
    # 组织层 / 应用层 rule：manifest v2 不再写；解析为可选，缺省空串。
    rule_organization_rel: str = ""
    rule_application_rel: str = ""
    # routing：8 个 auxiliary 槽位到模型名的映射；缺省空 dict（全部走主模型）。
    routing: dict = field(default_factory=dict)
    # skills：版本 skill tar 的相对路径列表（相对 input_root）；缺省空 list。
    skills: list = field(default_factory=list)
    # knowledge：manager runtime API 配置；不包含 RAGFlow 凭证。
    knowledge_runtime_base_url: str = ""
    knowledge_app_token: str = ""
    # web_publish：静态站点发布 API 配置；不含时表示企业未开通发布能力。
    web_publish_runtime_base_url: str = ""
    web_publish_app_token: str = ""
    web_publish_base_domain: str = ""
    # app_language：应用界面语言，由 manager 写入 manifest（"en"/"zh"）；
    # 缺省空串表示未配置，渲染时回落 "en"。
    app_language: str = ""


def _require(d: dict, *path: str) -> Any:
    """逐层取值；任一层缺失抛 ManifestError，错误信息指明字段路径。"""
    cur: Any = d
    for k in path:
        if not isinstance(cur, dict) or k not in cur:
            raise ManifestError(f"manifest missing field: {'.'.join(path)}")
        cur = cur[k]
    if cur in (None, ""):
        raise ManifestError(f"manifest empty field: {'.'.join(path)}")
    return cur


def load(path: Union[str, Path]) -> Manifest:
    """读 manifest.yaml 并构造 Manifest；未知顶层字段忽略；routing / skills / 组织层 / 应用层 rule 可选。"""
    raw = yaml.safe_load(Path(path).read_text())
    if not isinstance(raw, dict):
        raise ManifestError("manifest yaml root must be a mapping")
    resources = raw.get("resources")
    rules = resources.get("rules") if isinstance(resources, dict) else None
    rules = rules if isinstance(rules, dict) else {}
    routing = raw.get("routing")
    skills = resources.get("skills") if isinstance(resources, dict) else None
    knowledge = raw.get("knowledge")
    knowledge = knowledge if isinstance(knowledge, dict) else {}
    # web_publish：可选段，缺失或非 dict 时视为空配置（企业未开通发布能力）。
    wp = raw.get("web_publish")
    wp = wp if isinstance(wp, dict) else {}
    # 从 app 节点读取可选字段 language；不存在或为空时缺省空串，渲染侧回落 "en"。
    app_section = raw.get("app") if isinstance(raw.get("app"), dict) else {}
    return Manifest(
        app_id=_require(raw, "app", "id"),
        app_name=_require(raw, "app", "name"),
        app_model=_require(raw, "app", "model"),
        openai_api_key=_require(raw, "credentials", "openai", "api_key"),
        openai_base_url=_require(raw, "credentials", "openai", "base_url"),
        persona_rel=_require(raw, "resources", "persona"),
        rule_platform_rel=_require(raw, "resources", "rules", "platform"),
        rule_organization_rel=str(rules.get("organization") or ""),
        rule_application_rel=str(rules.get("application") or ""),
        routing={str(k): str(v) for k, v in routing.items()} if isinstance(routing, dict) else {},
        skills=[str(s) for s in skills] if isinstance(skills, list) else [],
        knowledge_runtime_base_url=str(knowledge.get("runtime_base_url") or ""),
        knowledge_app_token=str(knowledge.get("app_token") or ""),
        web_publish_runtime_base_url=str(wp.get("runtime_base_url") or ""),
        web_publish_app_token=str(wp.get("app_token") or ""),
        web_publish_base_domain=str(wp.get("base_domain") or ""),
        app_language=str(app_section.get("language") or ""),
    )
