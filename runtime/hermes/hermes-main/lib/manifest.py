"""manager 写入的 manifest.yaml 解析与必填字段校验。

仅暴露 forward-compat 的字段视图：调用方拿到 Manifest 实例后访问命名属性，
未知字段被忽略；缺必填字段则抛 ManifestError，让 oc-entrypoint 直接退出 1。
"""

from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
from typing import Any, Union

import yaml


class ManifestError(Exception):
    """manifest 缺失必填字段或解析失败。"""


@dataclass(frozen=True)
class Manifest:
    """业务化视图。字段语义对应 spec §4.2。"""
    app_id: str
    app_name: str
    app_model: str
    openai_api_key: str
    openai_base_url: str
    persona_rel: str
    rule_platform_rel: str
    rule_organization_rel: str
    rule_application_rel: str


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
    """读 manifest.yaml 并构造 Manifest；未知顶层字段忽略。"""
    raw = yaml.safe_load(Path(path).read_text())
    if not isinstance(raw, dict):
        raise ManifestError("manifest yaml root must be a mapping")
    return Manifest(
        app_id=_require(raw, "app", "id"),
        app_name=_require(raw, "app", "name"),
        app_model=_require(raw, "app", "model"),
        openai_api_key=_require(raw, "credentials", "openai", "api_key"),
        openai_base_url=_require(raw, "credentials", "openai", "base_url"),
        persona_rel=_require(raw, "resources", "persona"),
        rule_platform_rel=_require(raw, "resources", "rules", "platform"),
        rule_organization_rel=_require(raw, "resources", "rules", "organization"),
        rule_application_rel=_require(raw, "resources", "rules", "application"),
    )
