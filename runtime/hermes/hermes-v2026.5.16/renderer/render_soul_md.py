"""渲染 SOUL.md。

结构（manifest v2，与 spec §6.2 一致）：
1. 固定 header（语言要求）
2. persona 段
3. 平台层 rules：## 平台层；空层跳过。组织层 / 应用层已并入助手版本的 persona，不再单独渲染。

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

def render(m: Manifest, input_root: Path, data_root: Path) -> str:
    """按 spec §6.2 拼装 SOUL.md。"""
    data_root.mkdir(parents=True, exist_ok=True)
    parts: list[str] = [HEADER]

    persona = (input_root / m.persona_rel).read_text() if (input_root / m.persona_rel).exists() else ""
    if persona.strip():
        parts.append(persona.rstrip() + "\n\n")

    # manifest v2：只保留平台层 prompt；组织层 / 应用层已并入助手版本的 persona。
    platform_path = input_root / m.rule_platform_rel
    if m.rule_platform_rel and platform_path.exists():
        body = platform_path.read_text().strip()
        if body:
            parts.append(f"## 平台层\n\n{body}\n\n")

    write_text(data_root / "SOUL.md", "".join(parts))
    return "SOUL.md"
