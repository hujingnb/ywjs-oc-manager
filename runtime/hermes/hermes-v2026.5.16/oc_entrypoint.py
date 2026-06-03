"""oc_entrypoint 辅助函数模块。

oc-entrypoint.py（连字符命名）无法直接被 import；把可被测试引用的函数抽到本模块，
oc-entrypoint.py 再 from oc_entrypoint import ... 使用。
"""

from __future__ import annotations

import json
from pathlib import Path


def ensure_builtin_manifest(
    data_root: Path,
    manifest_path: Path = Path("/opt/skills-builtin.json"),
) -> None:
    """首次启动时把 skills/ 下无 .oc-managed 标记的目录记为镜像内置基线，写 manifest_path。

    已存在则不动（保护基线，防止后续启动 skills/ 混入 managed 目录后重写）。

    render_skills.render 会向 manager 安装的 skill 目录写 .oc-managed 标记；
    本函数必须在 render 之前调用，才能抓到「只有镜像内置」的干净基线。

    格式：{"builtin": ["skill-a", "skill-b"]}，名单按字母排序、换行结尾。
    """
    # 清单已存在时不覆盖，避免后续启动（skills/ 已混入 managed）覆盖初始基线。
    if manifest_path.exists():
        return

    skills_dir = data_root / "skills"
    if skills_dir.exists():
        # 仅收集目录条目，且不含 .oc-managed 标记（镜像内置 skill 无该标记）。
        names = sorted(
            d.name
            for d in skills_dir.iterdir()
            if d.is_dir() and not (d / ".oc-managed").exists()
        )
    else:
        # skills/ 不存在（纯空镜像），内置列表为空。
        names = []

    manifest_path.write_text(json.dumps({"builtin": names}) + "\n")
