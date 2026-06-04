"""渲染 Hermes skills。

当前来源：
- manifest.knowledge 存在时生成固定 oc-kb skill，指引 Hermes 通过 manager runtime API 检索/写入知识库；
- manifest.resources.skills 列出的版本 skill tar 解压到 skills/ 下。

每个由 oc-entrypoint 管理的 skill 目录都写入 .oc-managed 标记文件；
每次渲染前先清掉所有含该标记的目录，再重新渲染，保证已删除/切换的 skill 不残留。
镜像内置 skill（无 .oc-managed 标记）永不触碰。
"""

from __future__ import annotations

import datetime as _dt
import json
import shutil
import tarfile
import zipfile
from pathlib import Path
from typing import List

from lib.atomic import write_text
from lib.manifest import Manifest

# OC_SKILL_MARKER 是 oc-entrypoint 安装的 skill 目录里的隐藏标记文件名。
# 含该文件的目录由 oc-entrypoint 管理，每次渲染前清空重建；不含的视为镜像内置 skill，永不触碰。
OC_SKILL_MARKER = ".oc-managed"


def render(m: Manifest, input_root: Path, data_root: Path) -> List[str]:
    """渲染 skill：先清理上次 oc-entrypoint 安装的 skill，再渲染 oc-kb 与版本 skill tar。"""
    skills_root = data_root / "skills"
    _wipe_managed_skills(skills_root)
    outputs: list[str] = []
    outputs.extend(_render_runtime_knowledge_skill(m, skills_root))
    outputs.extend(_extract_version_skills(m.skills or [], input_root, skills_root))
    return outputs


def _wipe_managed_skills(skills_root: Path) -> None:
    """删掉 skills_root 下所有含 .oc-managed 标记的目录（上次 oc-entrypoint 安装的 skill）。"""
    if not skills_root.exists():
        return
    for child in sorted(skills_root.iterdir()):
        if child.is_dir() and (child / OC_SKILL_MARKER).exists():
            shutil.rmtree(child)


def _write_marker(skill_dir: Path, source: str) -> None:
    """在一个 skill 目录里写 .oc-managed 标记，记录来源与安装时间。"""
    payload = {
        "source": source,
        "installed_at": _dt.datetime.utcnow().replace(microsecond=0).isoformat() + "Z",
    }
    write_text(skill_dir / OC_SKILL_MARKER, json.dumps(payload, ensure_ascii=False) + "\n")


def _render_runtime_knowledge_skill(m: Manifest, skills_root: Path) -> List[str]:
    """manifest 含 knowledge 配置时生成固定 oc-kb skill；token 只进环境变量，不写入 SKILL.md。"""
    if not (m.knowledge_runtime_base_url and m.knowledge_app_token):
        return []
    skill_dir = skills_root / "oc-kb"
    skill_dir.mkdir(parents=True, exist_ok=True)
    write_text(skill_dir / "SKILL.md", _OC_KB_SKILL_MD)
    _write_marker(skill_dir, "runtime-knowledge")
    return ["skills/oc-kb/SKILL.md"]


def _is_safe_member_path(name: str) -> bool:
    """校验 tar 条目路径在解压目标内、不越界（不含 .. 段、非绝对路径）。"""
    from pathlib import PurePosixPath
    p = PurePosixPath(name)
    if p.is_absolute():
        return False
    parts = p.parts
    return ".." not in parts and len(parts) > 0


def _extract_tar(archive_path: Path, dest: Path, rel: str) -> None:
    """解压一个 tar 归档到 dest 目录（扁平契约：归档内容直接落到 dest 下）。

    双重防护：先逐条 _is_safe_member_path 校验路径越界，再用 extractall(filter="data")
    在解压时拒绝 symlink/hardlink 越界条目。
    """
    with tarfile.open(archive_path, "r") as tf:
        for member in tf.getmembers():
            if not _is_safe_member_path(member.name):
                raise ValueError(f"skill tar 含越界路径条目: {member.name} ({rel})")
        # filter="data" 在 extractall 内部再校验每个成员（含 symlink/hardlink 的 linkname），
        # 拒绝越界条目；与上面逐条 _is_safe_member_path 形成双重防护。
        tf.extractall(dest, filter="data")


def _extract_zip(archive_path: Path, dest: Path, rel: str) -> None:
    """解压一个 zip 归档到 dest 目录（扁平契约：归档内容直接落到 dest 下）。

    zip 标准库无 filter 机制，逐条用 _is_safe_member_path 校验路径，
    拒绝含 .. 段或绝对路径的条目（zip-slip 防护）。
    """
    with zipfile.ZipFile(archive_path, "r") as zf:
        for name in zf.namelist():
            if not _is_safe_member_path(name):
                raise ValueError(f"skill zip 含越界路径条目: {name} ({rel})")
        # 逐条提取，避免 extractall 跳过路径校验（部分实现不校验）。
        for name in zf.namelist():
            zf.extract(name, dest)


def _extract_version_skills(skill_rels: List[str], input_root: Path, skills_root: Path) -> List[str]:
    """解压 manifest.skills 列出的版本 skill 归档（tar/zip）到 skills_root/<name>/，并补 .oc-managed 标记。

    扁平契约（与 oc-ops install_skill 一致）：归档内 SKILL.md 等内容直接位于归档顶层，
    不含再套一层 <name>/ 目录。skill 目录名取归档文件名去扩展名
    （resources/skills/oc-hello.tar → oc-hello），解到 skills_root/<name>/，
    使 hermes 递归扫描到 <name>/SKILL.md 时识别为一个 skill。

    历史教训：旧实现把归档解到 skills_root 顶层并取「归档内顶层目录名」做 skill 目录，
    要求归档内含 <name>/ 目录；但平台库上传与 oc-ops 运行时安装都用扁平归档，
    导致扁平 tar 的 SKILL.md 散落到 skills_root/SKILL.md、skill 目录建不出来、对账永远 pending。
    """
    outputs: list[str] = []
    skills_root.mkdir(parents=True, exist_ok=True)
    for rel in skill_rels:
        archive_path = input_root / rel
        if not archive_path.exists():
            raise FileNotFoundError(f"版本 skill 归档不存在: {rel}")
        # skill 目录名取归档文件名（去扩展名）：oc-hello.tar / oc-hello.zip → oc-hello。
        name = Path(rel).stem
        skill_dir = skills_root / name
        # 幂等兜底：_wipe_managed_skills 已清掉上次的 .oc-managed 目录，这里再防同名残留。
        if skill_dir.exists():
            shutil.rmtree(skill_dir)
        skill_dir.mkdir(parents=True, exist_ok=True)
        # 按扩展名决定解压方式；Path.suffix 返回含点的后缀，如 ".zip"、".tar"。
        if archive_path.suffix.lower() == ".zip":
            _extract_zip(archive_path, skill_dir, rel)
        else:
            _extract_tar(archive_path, skill_dir, rel)
        _write_marker(skill_dir, "version-skill")
        outputs.append(f"skills/{name}/")
    return outputs


_OC_KB_SKILL_MD = """---
name: oc-kb
description: Search the organization and current app knowledge base through manager, and add local reports to the current app knowledge base.
---

# oc-kb

Use this skill when a user asks questions that may depend on organization policy, product documentation, app-specific rules, or files previously added to the knowledge base.

Commands:

- `oc-kb search "<question>" --top-k 8` searches both the current app knowledge base and the organization knowledge base. App results have higher priority than organization results.
- `oc-kb add relative/path.md` uploads an existing workspace file into the current app knowledge base. Absolute paths, parent directory traversal, and directories are rejected.

Do not call RAGFlow directly and do not ask for RAGFlow credentials. The `oc-kb` command talks only to manager runtime APIs using the app-scoped token injected by the container entrypoint.
"""
