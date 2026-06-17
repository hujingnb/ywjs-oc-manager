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

# 知识库使用指引。采用「无条件先查知识库」语气：Hermes 容器内置上百个 skill，
# 若不在 system prompt 把 oc-kb 抬成每轮对话的强制第一步，模型在面对"查资料/查文档"类
# 问题时会被其它 skill（技能文档、网络搜索）抢走，甚至凭"这问题应该不在库里"的主观判断
# 直接跳过检索，导致知识库形同虚设。因此本段不再让模型先判断"是否依赖知识库"，而是要求
# 对每一次提问都先执行 oc-kb search，再据结果决定是否动用其它信息源。
# 仅当 manifest.knowledge 配置齐全（oc-kb skill 已注入）时才渲染本段，
# 未接入知识库的实例不渲染，避免模型误调不存在的 skill。
KNOWLEDGE_GUIDE = (
    "## 知识库（最高优先级信息源）\n\n"
    "你接入了一个知识库，通过内置的 `oc-kb` skill 访问，它是你检索资料的**第一入口**。\n\n"
    "- **强制规则：对用户的每一次提问，你的第一个动作都必须是 `oc-kb search \"<用户问题>\"`，\n"
    "  不得跳过、不得先用网络搜索 / 技能文档 / 凭记忆作答，哪怕你主观判断知识库里没有相关内容；**\n"
    "- 只有在 `oc-kb search` 返回后，你才能决定如何作答：命中则依据检索结果回答；\n"
    "  确认无相关内容时，才考虑其它信息源，并在回复中说明该结论来自知识库之外；\n"
    "- 禁止出现\"让我用浏览器搜一下\"\"我直接查一下网络\"这类先于 `oc-kb search` 就调用其它信息源的行为；\n"
    "- 当用户要求把工作目录中的文件 / 报告加入知识库时，用 `oc-kb add <workspace 相对路径>`；\n"
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

    # 知识库指引：与 render_skills 渲染 oc-kb skill 的判定条件保持一致
    # （manifest.knowledge runtime_base_url 与 app_token 同时存在），
    # 确保"system prompt 提到 oc-kb"与"oc-kb skill 实际可用"两件事同步出现。
    if m.knowledge_runtime_base_url and m.knowledge_app_token:
        parts.append(KNOWLEDGE_GUIDE)

    write_text(data_root / "SOUL.md", "".join(parts))
    return "SOUL.md"
