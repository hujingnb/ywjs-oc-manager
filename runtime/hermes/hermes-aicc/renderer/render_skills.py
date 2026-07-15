"""渲染 AICC 的运行时 Skill 状态。

客服镜像只允许 Docker 构建期审核并内置的只读 Skill。启动阶段不接受 manifest
携带的 bootstrap 归档，也不生成带写入/发布语义的临时 Skill；这样新 Pod 始终具有
一致、可审计的能力集合。
"""

from __future__ import annotations

import shutil
from pathlib import Path

from lib.manifest import Manifest

# OC_SKILL_MARKER 标记历史上由 entrypoint 动态安装的目录；内置客服 Skill 没有该标记。
OC_SKILL_MARKER = ".oc-managed"


def render(m: Manifest, input_root: Path, data_root: Path) -> list[str]:
    """清理旧 managed Skill；AICC 不从 manifest 追加任何可变 Skill。"""
    # 参数仍保留以兼容统一 renderer 调用契约；AICC 有意不读取 manifest 的技能配置。
    del m, input_root
    _wipe_managed_skills(data_root / "skills")
    return []


def _wipe_managed_skills(skills_root: Path) -> None:
    """删除历史动态安装目录，避免旧镜像版本遗留的写入型 Skill 被扫描。"""
    if not skills_root.exists():
        return
    for child in sorted(skills_root.iterdir()):
        if child.is_dir() and (child / OC_SKILL_MARKER).exists():
            shutil.rmtree(child)
