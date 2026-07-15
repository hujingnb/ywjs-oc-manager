"""验证构建上下文中的 AICC 内置客服 Skill 白名单。"""

from pathlib import Path


def test_builtin_skill_directory_contains_only_reviewed_customer_skills() -> None:
    # Docker 会把此目录整体复制到 Hermes skill 根目录，因此目录名本身就是静态能力白名单。
    root = Path(__file__).resolve().parent.parent / "skills"
    names = sorted(path.name for path in root.iterdir() if path.is_dir())
    assert names == ["aicc-customer-answer", "aicc-lead-analysis", "aicc-safe-web-research"]
    assert all(name.startswith("aicc-") for name in names)


def test_customer_answer_skill_does_not_document_legacy_write_execution() -> None:
    # 知识答复 Skill 只能调用 Task 2 提供的只读搜索工具，不能重新引入旧 CLI 写入或代码执行路径。
    body = (Path(__file__).resolve().parent.parent / "skills" / "aicc-customer-answer" / "SKILL.md").read_text(
        encoding="utf-8",
    )
    assert "oc-kb add" not in body
    assert "execute_code" not in body
    assert "aicc_knowledge_search" in body
