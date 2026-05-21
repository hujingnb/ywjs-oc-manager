"""跨 variant 数据迁移 dispatch。

当前 variant 是 hermes-v2026.5.7。它由历史 hermes-main 目录重命名而来，
因此允许 hermes-main → hermes-v2026.5.7 作为 no-op 迁移。
未来版本号可能包含 "."，迁移模块名必须先规整为 Python-safe 后缀。
"""

from __future__ import annotations

import importlib
from pathlib import Path
from typing import Optional

LEGACY_NOOP_PREV_VARIANTS = {"hermes-main"}


def run(prev_variant: Optional[str], curr_variant: str, data_root: Path) -> Optional[dict]:
    """根据 prev/curr variant 决定是否需要迁移。

    返回 None 表示跳过迁移；非 None 返回迁移摘要（写入 .oc-state.last_migrate_from）。
    迁移失败抛异常，调用方（oc-entrypoint）退出码 1，并保证 data_root 已被 migrator 原子处理。
    """
    if prev_variant is None or prev_variant == curr_variant:
        return None
    if curr_variant == "hermes-v2026.5.7" and prev_variant in LEGACY_NOOP_PREV_VARIANTS:
        return {"from": prev_variant, "to": curr_variant, "mode": "noop_rename"}
    module_name = f"migrator.from_{_migration_module_suffix(prev_variant)}"
    try:
        mod = importlib.import_module(module_name)
    except ModuleNotFoundError as e:
        raise NotImplementedError(
            f"no migrator path from {prev_variant} → {curr_variant}; "
            f"please ship a {module_name} module"
        ) from e
    return mod.run(data_root)


def _migration_module_suffix(variant: str) -> str:
    """把 variant 名转换为可用于 Python module 的后缀。"""
    return variant.replace("-", "_").replace(".", "_")
