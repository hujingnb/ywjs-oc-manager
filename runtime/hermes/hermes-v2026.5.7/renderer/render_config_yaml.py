"""把 manifest 渲染为本 variant 期望的 hermes config.yaml。

manifest v2：auxiliary 8 个槽位按 manifest.routing 渲染——指定模型的槽位走
custom + 该模型，未指定的走 { provider: main }。base_url 拼 /v1 由本 variant 决定。
"""

from __future__ import annotations

from pathlib import Path

import yaml

from lib.atomic import write_text
from lib.manifest import Manifest

# AUXILIARY_SLOTS 是智能路由的 8 个 auxiliary 槽位，顺序固定，与 manager 端约定一致。
AUXILIARY_SLOTS = [
    "vision", "compression", "web_extract", "session_search",
    "title_generation", "approval", "skills_hub", "mcp",
]


def _build_auxiliary(m: Manifest, base_url: str) -> dict:
    """按 manifest.routing 构造 auxiliary 段：指定模型走 custom，未指定走 main。"""
    aux: dict = {}
    routing = m.routing or {}
    for slot in AUXILIARY_SLOTS:
        model = str(routing.get(slot) or "").strip()
        if model:
            aux[slot] = {
                "provider": "custom", "model": model,
                "base_url": base_url, "api_key": m.openai_api_key,
            }
        else:
            aux[slot] = {"provider": "main"}
    return aux


def render(m: Manifest, data_root: Path) -> str:
    """渲染 config.yaml 到 data_root/config.yaml，返回相对路径。"""
    data_root.mkdir(parents=True, exist_ok=True)
    base_url = m.openai_base_url.rstrip("/") + "/v1"
    config = {
        "model": {
            "default": m.app_model, "provider": "custom",
            "base_url": base_url, "api_key": m.openai_api_key,
        },
        "auxiliary": _build_auxiliary(m, base_url),
        "memory": {
            "memory_enabled": True, "user_profile_enabled": True,
            "memory_char_limit": 2200, "user_char_limit": 1375,
        },
        "terminal": {
            "backend": "local", "cwd": "/opt/data/workspace",
            "timeout": 180, "lifetime_seconds": 300,
        },
    }
    header = "# Hermes 配置 - 由 oc-entrypoint 在容器启动时渲染（manifest v2）\n"
    body = header + yaml.safe_dump(config, allow_unicode=True, sort_keys=False)
    write_text(data_root / "config.yaml", body)
    return "config.yaml"
