"""渲染 SOUL.md。

结构（与 spec §6.2 一致）：
1. 固定 header（语言要求）
2. persona 段
3. 三层 rules：## 平台层 / ## 组织层 / ## 应用层；空层跳过
4. 知识库 always-on inline：应用级在前、组织级在后，单文件 > 8 KiB 截断并提示完整版位置

manager 端 prompt 占位符已替换完毕，本 renderer 只做拼装。
"""

from __future__ import annotations

from pathlib import Path

from lib.atomic import write_text
from lib.manifest import Manifest

HEADER = (
    "# Agent Identity (SOUL.md)\n\n"
    "本文件由 oc-entrypoint 在容器启动时生成，Hermes 启动后注入 system prompt。\n\n"
    "## 语言要求\n\n"
    "始终用简体中文回复用户。即使用户用英文或其他语言提问，也请用中文作答\n"
    "（代码、命令、API 名称、错误码等技术标识保留英文原文）。\n\n"
)

INLINE_LIMIT = 8 * 1024  # 单知识库文件 inline 上限 8 KiB


def render(m: Manifest, input_root: Path, data_root: Path) -> str:
    """按 spec §6.2 拼装 SOUL.md。"""
    data_root.mkdir(parents=True, exist_ok=True)
    parts: list[str] = [HEADER]

    persona = (input_root / m.persona_rel).read_text() if (input_root / m.persona_rel).exists() else ""
    if persona.strip():
        parts.append(persona.rstrip() + "\n\n")

    for title, rel in (
        ("平台层", m.rule_platform_rel),
        ("组织层", m.rule_organization_rel),
        ("应用层", m.rule_application_rel),
    ):
        path = input_root / rel
        if not path.exists():
            continue
        body = path.read_text().strip()
        if not body:
            continue
        parts.append(f"## {title}\n\n{body}\n\n")

    inline = _collect_inline(input_root)
    if inline:
        parts.append("## 知识库（always-on 摘要）\n\n")
        parts.extend(inline)

    write_text(data_root / "SOUL.md", "".join(parts))
    return "SOUL.md"


def _collect_inline(input_root: Path) -> list[str]:
    """按「应用级在前、组织级在后」顺序读取 knowledge/ 主副本树，单文件 8 KiB 截断。"""
    res = input_root / "resources" / "knowledge"
    chunks: list[str] = []
    for scope, label in (("app", "应用级"), ("org", "组织级")):
        base = res / scope
        if not base.exists():
            continue
        for f in sorted(base.rglob("*.md")):
            rel = f.relative_to(base).as_posix()
            body = f.read_text()
            slug = _slug(rel)
            chunks.append(f"### [{label}] {rel}\n\n")
            if len(body) > INLINE_LIMIT:
                chunks.append(body[:INLINE_LIMIT])
                chunks.append(f"\n\n> （内容已截断；完整版见 skills/kb-{scope}-{slug}/SKILL.md）\n\n")
            else:
                chunks.append(body)
                chunks.append("\n\n")
    return chunks


def _slug(rel: str) -> str:
    """与 render_skills 共用的 slug 算法的简化版：仅用于截断提示文案。

    T5 实施前 render_skills 不存在，本函数走 fallback；T5 上线后自动用真正算法。
    """
    try:
        from renderer.render_skills import slugify_knowledge_path
        return slugify_knowledge_path(rel)
    except ImportError:
        return rel.replace("/", "-").replace(".md", "")
