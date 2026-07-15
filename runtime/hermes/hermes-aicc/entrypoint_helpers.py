"""entrypoint_helpers 辅助函数模块。

oc-entrypoint.py（连字符命名）无法直接被 import；把可被测试引用的函数抽到本模块，
oc-entrypoint.py 再 from entrypoint_helpers import ... 使用。
"""

from __future__ import annotations

import hashlib
import os
import shutil
from pathlib import Path

import yaml

from aicc_tools.policy import validate_skill_capabilities

# AICC_BUILTIN_SKILLS_DIR 是镜像层中不可被 /opt/data emptyDir 覆盖的只读 Skill 源目录。
AICC_BUILTIN_SKILLS_DIR = Path("/opt/oc-aicc-skills")


def sync_aicc_builtin_skills(
    data_root: Path,
    manifest_capabilities: frozenset[str],
    source_root: Path | None = None,
) -> None:
    """校验并同步镜像内置客服 Skill 到共享卷。

    Kubernetes 的 emptyDir 会覆盖 /opt/data，因此内置 Skill 不能直接放在该目录。每次
    启动都先从镜像只读层完整覆盖到 data_root/skills，既保证新 Pod 可见，也消除旧镜像
    遗留的通用 Skill。复制前校验所有 frontmatter，任一越权 Skill 均失败关闭。
    """
    source = source_root or Path(os.environ.get("OC_BUILTIN_SKILLS_DIR", AICC_BUILTIN_SKILLS_DIR))
    if not source.is_dir():
        raise ValueError(f"AICC_BUILTIN_SKILLS_MISSING: {source}")
    skill_dirs = sorted(path for path in source.iterdir() if path.is_dir())
    if not skill_dirs:
        raise ValueError("AICC_BUILTIN_SKILLS_MISSING: no skills")
    for skill_dir in skill_dirs:
        _validate_aicc_skill_frontmatter(skill_dir / "SKILL.md", manifest_capabilities)

    destination = data_root / "skills"
    destination.mkdir(parents=True, exist_ok=True)
    # AICC 不存在运行期可安装 Skill；删除所有旧目录避免旧版本的通用 Skill 留在扫描范围。
    for child in destination.iterdir():
        if child.is_dir():
            shutil.rmtree(child)
    for skill_dir in skill_dirs:
        shutil.copytree(skill_dir, destination / skill_dir.name)
    # 复制结果可能随镜像升级变化，基线必须重新生成而不能沿用旧哈希。
    (destination / ".bundled_manifest").unlink(missing_ok=True)


def _validate_aicc_skill_frontmatter(skill_md: Path, manifest_capabilities: frozenset[str]) -> None:
    """解析并校验一个内置 Skill 的能力声明，拒绝缺失、畸形与越权声明。"""
    if not skill_md.is_file():
        raise ValueError(f"AICC_SKILL_CAPABILITY_INVALID: missing SKILL.md ({skill_md.parent.name})")
    body = skill_md.read_text(encoding="utf-8")
    if not body.startswith("---\n"):
        raise ValueError(f"AICC_SKILL_CAPABILITY_INVALID: missing frontmatter ({skill_md.parent.name})")
    frontmatter, separator, _ = body[4:].partition("\n---\n")
    if not separator:
        raise ValueError(f"AICC_SKILL_CAPABILITY_INVALID: unterminated frontmatter ({skill_md.parent.name})")
    metadata = yaml.safe_load(frontmatter)
    declared = metadata.get("aicc_capabilities") if isinstance(metadata, dict) else None
    if not isinstance(declared, list) or not all(isinstance(capability, str) for capability in declared):
        raise ValueError(f"AICC_SKILL_CAPABILITY_INVALID: aicc_capabilities ({skill_md.parent.name})")
    validate_skill_capabilities(declared, manifest_capabilities)


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
