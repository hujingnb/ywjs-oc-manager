"""扫 input/resources/knowledge/{org,app}/* 生成 skills/kb-{scope}-{slug}/SKILL.md。

算法搬运自旧 manager 端 hermes/skills.go 的 SlugifyKnowledgePath，
保持对同一文件路径生成相同 slug。
"""

from __future__ import annotations

import hashlib
import re
from pathlib import Path
from typing import List

from lib.atomic import write_text

# slug 仅含小写字母数字与连字符；首尾不能是连字符。
SLUG_PATTERN = re.compile(r"^[a-z0-9]+(-[a-z0-9]+)*$")


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


def render(input_root: Path, data_root: Path) -> List[str]:
    """生成每个知识库文件对应的 SKILL.md，返回写入相对路径列表。"""
    skills_root = data_root / "skills"
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
            outputs.append(f"skills/{dir_name}/SKILL.md")
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
