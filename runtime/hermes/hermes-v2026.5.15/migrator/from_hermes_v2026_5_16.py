"""hermes-v2026.5.16 → hermes-v2026.5.15 的迁移。

两个 variant 渲染逻辑完全一致，数据无需迁移，no-op。
"""

from __future__ import annotations

from pathlib import Path


def run(data_root: Path) -> dict:
    """no-op 迁移：不改动 data_root，仅返回迁移摘要。"""
    return {"from": "hermes-v2026.5.16", "to": "hermes-v2026.5.15", "mode": "noop"}
