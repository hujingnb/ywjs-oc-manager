"""跨 variant 数据迁移 dispatch。

约定：from_<prev_variant>.py 暴露 run(data_root: Path) -> dict 函数；
本 variant（hermes-main）首版无任何 from_*.py，所以遇到任何已知 prev 都抛。
未来新 variant（如 hermes-v0.5）需新增 from_hermes-main.py 等模块。
"""

from __future__ import annotations

import importlib
from pathlib import Path
from typing import Optional


def run(prev_variant: Optional[str], curr_variant: str, data_root: Path) -> Optional[dict]:
    """根据 prev/curr variant 决定是否需要迁移。

    返回 None 表示跳过迁移；非 None 返回迁移摘要（写入 .oc-state.last_migrate_from）。
    迁移失败抛异常，调用方（oc-entrypoint）退出码 1，并保证 data_root 已被 migrator 原子处理。
    """
    if prev_variant is None or prev_variant == curr_variant:
        return None
    module_name = f"migrator.from_{prev_variant.replace('-', '_')}"
    try:
        mod = importlib.import_module(module_name)
    except ModuleNotFoundError as e:
        raise NotImplementedError(
            f"no migrator path from {prev_variant} → {curr_variant}; "
            f"please ship a {module_name} module"
        ) from e
    return mod.run(data_root)
