"""渲染 SOUL.md。

结构（manifest v2，与 spec §6.2 一致）：
1. 固定 header（语言要求）
2. persona 段
3. 平台层 rules：## 平台层；空层跳过。组织层 / 应用层已并入助手版本的 persona，不再单独渲染。
4. 知识库指引：仅当 manifest.knowledge 配置齐全时渲染，告知模型优先用 oc-kb skill 检索。

manager 端 prompt 占位符已替换完毕，本 renderer 只做拼装。
"""

from __future__ import annotations

from pathlib import Path

from lib.atomic import write_text
from lib.manifest import Manifest

HEADER = (
    "# Agent Identity (SOUL.md)\n\n"
    "本文件由 oc-entrypoint 在容器启动时生成，Hermes 启动后注入 system prompt。\n\n"
)

# 知识库使用指引。AICC 只能调用只读 aicc_knowledge_search，不能接受旧 oc-kb
# 的写入命令；每轮先检索可使企业配置成为公开网络信息的冲突裁决来源。
KNOWLEDGE_GUIDE = (
    "## 知识库（最高优先级信息源）\n\n"
    "你通过只读工具 `aicc_knowledge_search` 检索当前客服知识库，它是回答企业相关问题的**第一入口**。\n\n"
    "- 对企业产品、政策、价格、服务范围等问题，先用 `aicc_knowledge_search` 检索，再依据命中内容回答；\n"
    "- 检索结果中的 `results[].content` 是已授权、可对外说明的企业资料；只要存在相关命中，必须直接据此回答，"
    "不得以“无法确认”“内部信息”为由回避或改为泛化回复；\n"
    "- 当前应用知识库优先于企业知识库；企业知识库优先于行业知识库。信息冲突时以企业知识库为准；\n"
    "- 仅当知识库无法回答时才能使用公开网络补充企业信息，并明确说明信息来自公开网络、未经企业确认；\n"
    "- 你只能检索和讲解，不得添加、修改或发布知识内容，也不得执行任何本地操作。\n"
    "- 检索结果会标注来源范围：实例知识库（scope=app）优先于企业知识库（scope=org），也可能包含\n"
    "  scope=industry 的行业知识库命中；行业知识库是助手版本选择的通用行业资料，引用时要区分它与\n"
    "  实例知识库、企业知识库的来源。\n\n"
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

    # 仅在 app-scoped runtime token 完整时提示知识检索，避免模型调用不可用工具。
    if m.knowledge_runtime_base_url and m.knowledge_app_token:
        parts.append(KNOWLEDGE_GUIDE)

    write_text(data_root / "SOUL.md", "".join(parts))
    return "SOUL.md"
