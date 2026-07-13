"""跨 variant 数据迁移。

迁移以 data_root 自身记录的版本为准，而非按来源版本名 dispatch 到 from_<prev>
模块：oc-entrypoint 读 data_root/.oc-state.json 的 image_variant 作为该目录的
「当前数据版本」（prev_variant）传入，迁移完成后再把 image_variant 写回（即在
目录中把版本标记更新为新版本）。本模块据此与运行镜像版本（curr_variant）比对——
不一致就对 data_root 做文件检测，把目录里不兼容的旧结构原地转换成新版本布局。

文件检测逐项判断、只转换确实存在的旧结构，对升级 / 降级、任意来源版本通用，
不需要为每个来源版本单独写迁移模块。
"""

from __future__ import annotations

from pathlib import Path
from typing import List, Optional


def run(prev_variant: Optional[str], curr_variant: str, data_root: Path) -> Optional[dict]:
    """按目录记录的版本决定是否迁移。

    prev_variant 为 None（目录无版本标记，首次启动）或与 curr_variant 相同（同
    版本重启）时无需迁移，返回 None。否则目录处在不同版本，对 data_root 做文件
    检测并把不兼容数据迁移成 curr_variant 的布局，返回迁移摘要（其非 None 触发
    oc-entrypoint 记录 .oc-state.last_migrate_from）。mode 反映实际是否发生转换：
    检测到并迁移了旧结构为 "migrated"，未命中任何不兼容结构为 "noop"。
    """
    if prev_variant is None or prev_variant == curr_variant:
        return None
    migrated = _migrate(data_root)
    return {
        "from": prev_variant,
        "to": curr_variant,
        "mode": "migrated" if migrated else "noop",
        "migrated": migrated,
    }


def _migrate(data_root: Path) -> List[str]:
    """对 data_root 做文件检测，把不兼容的旧结构原地迁移成当前版本布局。

    返回已迁移条目的名称列表（供摘要与日志佐证）；未命中任何不兼容结构返回空列表。
    通过逐项检测实际文件而非比对版本号来驱动，故对任意来源版本、升级或降级一致成立。

    当前所有 variant 的持久化数据布局完全一致（.oc-state.json / workspace /
    sessions / weixin），不存在跨 variant 的不兼容 on-disk 格式，文件检测无命中、
    原地不动。后续引入不兼容格式时，在此追加「检测旧结构 → 原地转换 → 记入返回列表」
    的分支即可，无需改动 dispatch 或新增来源版本模块。
    """
    migrated: List[str] = []
    return migrated
