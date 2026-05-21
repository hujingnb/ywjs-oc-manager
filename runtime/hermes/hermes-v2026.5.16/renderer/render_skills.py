"""扫描 input/resources/knowledge/{org,app}/* 生成 skills/kb-{scope}-{slug}/SKILL.md，
同时解压 manifest.skills 列出的版本 skill tar 到 skills/ 下。

每个由 oc-entrypoint 管理的 skill 目录都写入 .oc-managed 标记文件；
每次渲染前先清掉所有含该标记的目录，再重新渲染，保证已删除/切换的 skill 不残留。
镜像内置 skill（无 .oc-managed 标记）永不触碰。
"""

from __future__ import annotations

import datetime as _dt
import hashlib
import json
import re
import shutil
import tarfile
from pathlib import Path
from typing import List

from lib.atomic import write_text
from lib.manifest import Manifest

# slug 仅含小写字母数字与连字符；首尾不能是连字符。
SLUG_PATTERN = re.compile(r"^[a-z0-9]+(-[a-z0-9]+)*$")

# OC_SKILL_MARKER 是 oc-entrypoint 安装的 skill 目录里的隐藏标记文件名。
# 含该文件的目录由 oc-entrypoint 管理，每次渲染前清空重建；不含的视为镜像内置 skill，永不触碰。
OC_SKILL_MARKER = ".oc-managed"


def slugify_knowledge_path(rel: str) -> str:
    """规范化为 slugPattern；纯非 ASCII 路径回落到 sha256 短哈希。"""
    if not rel:
        return _fallback(rel)
    # 仅当最后一段含 '.' 时才视为扩展名；纯目录路径不做扩展名裁剪。
    base = rel.rsplit(".", 1)[0] if "." in rel.rsplit("/", 1)[-1] else rel
    chars: list[str] = []
    for c in base:
        if "a" <= c <= "z" or "0" <= c <= "9":
            chars.append(c)
        elif "A" <= c <= "Z":
            chars.append(c.lower())
        else:
            chars.append("-")
    s = "".join(chars)
    while "--" in s:
        s = s.replace("--", "-")
    s = s.strip("-")
    if not s or not SLUG_PATTERN.match(s):
        return _fallback(rel)
    return s


def _fallback(rel: str) -> str:
    # 与 manager 端 slugFallback 一致：sha256 前 6 字节 hex = 12 个字符。
    h = hashlib.sha256(rel.encode()).hexdigest()
    return f"kb-{h[:12]}"


def render(m: Manifest, input_root: Path, data_root: Path) -> List[str]:
    """渲染 skill：先清理上次 oc-entrypoint 安装的 skill，再渲染知识库 kb-* 与版本 skill tar。

    返回写入的相对路径列表。镜像内置 skill（无 .oc-managed 标记）不受影响。
    """
    skills_root = data_root / "skills"
    _wipe_managed_skills(skills_root)
    outputs: list[str] = []
    outputs.extend(_render_knowledge_skills(input_root, skills_root))
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


def _render_knowledge_skills(input_root: Path, skills_root: Path) -> List[str]:
    """扫 input/resources/knowledge/{org,app}/* 生成 kb-* skill，每个目录补 .oc-managed 标记。"""
    outputs: list[str] = []
    for scope in ("org", "app"):
        base = input_root / "resources" / "knowledge" / scope
        if not base.exists():
            continue
        for f in sorted(base.rglob("*.md")):
            rel = f.relative_to(base).as_posix()
            slug = slugify_knowledge_path(rel)
            dir_name = f"kb-{scope}-{slug}"
            target_dir = skills_root / dir_name
            target_dir.mkdir(parents=True, exist_ok=True)
            body = _render_skill_md(scope, dir_name, rel, f.read_text())
            write_text(target_dir / "SKILL.md", body)
            _write_marker(target_dir, "knowledge")
            outputs.append(f"skills/{dir_name}/SKILL.md")
    return outputs


def _is_safe_member_path(name: str) -> bool:
    """校验 tar 条目路径在解压目标内、不越界（不含 .. 段、非绝对路径）。"""
    from pathlib import PurePosixPath
    p = PurePosixPath(name)
    if p.is_absolute():
        return False
    parts = p.parts
    return ".." not in parts and len(parts) > 0


def _extract_version_skills(skill_rels: List[str], input_root: Path, skills_root: Path) -> List[str]:
    """解压 manifest.skills 列出的版本 skill tar 到 skills_root，每个顶层目录补 .oc-managed 标记。"""
    outputs: list[str] = []
    skills_root.mkdir(parents=True, exist_ok=True)
    for rel in skill_rels:
        tar_path = input_root / rel
        if not tar_path.exists():
            raise FileNotFoundError(f"版本 skill tar 不存在: {rel}")
        top_dirs: set[str] = set()
        with tarfile.open(tar_path, "r") as tf:
            for member in tf.getmembers():
                if not _is_safe_member_path(member.name):
                    raise ValueError(f"skill tar 含越界路径条目: {member.name} ({rel})")
                if member.isreg() or member.isdir():
                    top = member.name.split("/", 1)[0]
                    if top:
                        top_dirs.add(top)
            tf.extractall(skills_root)
        for top in sorted(top_dirs):
            skill_dir = skills_root / top
            if skill_dir.is_dir():
                _write_marker(skill_dir, "version-skill")
                outputs.append(f"skills/{top}/")
    return outputs


def _render_skill_md(scope: str, dir_name: str, rel: str, body: str) -> str:
    # 沿用旧 manager 端 hermes/skills.go 的 frontmatter + body 模板。
    # heading 为 body 首个 markdown H1；非空表示用户文件自带标题。
    heading = _extract_heading(body)
    title = heading or rel
    if scope == "org":
        desc = (
            f"组织级知识库文件 {title}。介绍本组织业务、产品、政策、规则等权威信息。"
            "当用户的提问涉及组织业务、公司、产品、规则、政策、流程时，必须读取本 skill 获取最新内容，"
            "不要根据通用知识猜测。"
        )
    else:
        desc = (
            f"应用级知识库文件 {title}。包含本应用专属规则、话术、配置，优先级高于同名组织级知识。"
            "用户的任意提问都应先读取本 skill 确认是否有匹配规则；有则按本 skill 内容回答，"
            "无则回退到组织级或通用知识。"
        )
    # body 已自带 H1 时直接输出，避免「renderer 加的标题 + 文件自带标题」重复；
    # body 无 H1 时用相对路径补一个标题，保证 SKILL.md 正文有抬头。
    body_section = body if heading else f"# {title}\n\n{body}"
    return f"""---
name: {dir_name}
description: {desc}
scope: {scope}
---

{body_section}
"""


def _extract_heading(body: str) -> str:
    # 提取 markdown body 首个 # 开头行的标题文本（去 # 与空格）。
    for line in body.splitlines():
        s = line.strip()
        if s.startswith("#"):
            return s.lstrip("#").strip()
    return ""
