"""entrypoint_helpers 辅助函数模块。

oc-entrypoint.py（连字符命名）无法直接被 import；把可被测试引用的函数抽到本模块，
oc-entrypoint.py 再 from entrypoint_helpers import ... 使用。
"""

from __future__ import annotations

import hashlib
from pathlib import Path


def ensure_builtin_manifest(
    data_root: Path,
    manifest_path: Path | None = None,
) -> None:
    """首次启动时把 skills/ 下无 .oc-managed 标记的 skill 记为镜像内置基线。

    默认写入 data_root/skills/.bundled_manifest，使 hermes 主容器、oc-ops 和 ops sidecar
    都能从共享 data volume 读取同一份基线。已存在则不动，防止后续启动时把运行期
    managed / 自创 skill 混入内置基线。

    render_skills.render 会向 manager 安装的 skill 目录写 .oc-managed 标记；
    本函数必须在 render 之前调用，才能抓到「只有镜像内置」的干净基线。

    格式：每行 "<skill-name>:<sha256(SKILL.md)>"，按 skill name 排序。
    """
    skills_dir = data_root / "skills"
    if manifest_path is None:
        manifest_path = skills_dir / ".bundled_manifest"
    # 清单已存在时不覆盖，避免后续启动（skills/ 已混入 managed）覆盖初始基线。
    if manifest_path.exists():
        return
    manifest_path.parent.mkdir(parents=True, exist_ok=True)
    if skills_dir.exists():
        # 递归收集含 SKILL.md 的真实 skill 目录；category 容器目录自身无 SKILL.md，不入基线。
        entries = []
        for skill_md in sorted(skills_dir.rglob("SKILL.md")):
            skill_dir = skill_md.parent
            if (skill_dir / ".oc-managed").exists():
                continue
            name = _read_skill_name(skill_md, skill_dir.name)
            digest = hashlib.sha256(skill_md.read_bytes()).hexdigest()
            entries.append((name, digest))
    else:
        entries = []

    manifest_path.write_text(
        "".join(f"{name}:{digest}\n" for name, digest in sorted(entries)),
        encoding="utf-8",
    )


def _read_skill_name(skill_md: Path, fallback: str) -> str:
    """读取 SKILL.md frontmatter 的 name 字段；缺失或读取失败时回退目录名。"""
    try:
        for line in skill_md.read_text(encoding="utf-8", errors="replace").splitlines():
            stripped = line.strip()
            if stripped.startswith("name:"):
                name = stripped[len("name:"):].strip().strip('"').strip("'")
                return name or fallback
    except OSError:
        return fallback
    return fallback
