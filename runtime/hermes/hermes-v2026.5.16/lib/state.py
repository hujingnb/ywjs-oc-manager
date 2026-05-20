"""/opt/data/.oc-state.json 读写。

.oc-state.json 是镜像私有契约，manager 不读不写；spec §6.3。
未知字段保留，便于未来在不影响老逻辑前提下扩展。
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field, asdict
from pathlib import Path
from typing import List, Optional

STATE_FILE = ".oc-state.json"


@dataclass
class OcState:
    image_variant: Optional[str] = None
    last_render_at: Optional[str] = None
    last_migrate_from: Optional[str] = None
    manifest_sha256: Optional[str] = None
    renderer_outputs: List[str] = field(default_factory=list)


def read_state(data_root: Path) -> OcState:
    """读 .oc-state.json；缺失或损坏视为「首次启动」返回空 OcState。"""
    p = data_root / STATE_FILE
    if not p.exists():
        return OcState()
    try:
        raw = json.loads(p.read_text())
    except (json.JSONDecodeError, OSError):
        return OcState()
    if not isinstance(raw, dict):
        return OcState()
    return OcState(
        image_variant=raw.get("image_variant"),
        last_render_at=raw.get("last_render_at"),
        last_migrate_from=raw.get("last_migrate_from"),
        manifest_sha256=raw.get("manifest_sha256"),
        renderer_outputs=list(raw.get("renderer_outputs") or []),
    )


def write_state(data_root: Path, state: OcState) -> None:
    """以 atomic 写入 .oc-state.json。

    使用 from .atomic 间接依赖，避免与 atomic 模块循环引用。
    """
    from .atomic import write_text
    data_root.mkdir(parents=True, exist_ok=True)
    write_text(data_root / STATE_FILE, json.dumps(asdict(state), ensure_ascii=False, indent=2) + "\n")
