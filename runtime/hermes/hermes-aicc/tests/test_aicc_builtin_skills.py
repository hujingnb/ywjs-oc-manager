"""验证构建上下文中的 AICC 内置客服 Skill 白名单。"""

from pathlib import Path
import shutil

import yaml

from entrypoint_helpers import sync_aicc_builtin_skills


def test_builtin_skill_directory_contains_only_reviewed_customer_skills() -> None:
    # Docker 会把此目录整体复制到 Hermes skill 根目录，因此目录名本身就是静态能力白名单。
    root = Path(__file__).resolve().parent.parent / "skills"
    names = sorted(path.name for path in root.iterdir() if path.is_dir())
    assert names == ["aicc-customer-answer", "aicc-lead-analysis", "aicc-safe-web-research"]
    assert all(name.startswith("aicc-") for name in names)


def test_dockerfile_clears_upstream_skill_layout_before_copying_customer_skills() -> None:
    # 镜像层必须先清除 install.sh 在 /opt/data/skills 写入的通用 Skill，再保存只读源目录。
    dockerfile = (Path(__file__).resolve().parent.parent / "Dockerfile").read_text(encoding="utf-8")
    assert "rm -rf /opt/data/skills /opt/oc-aicc-skills" in dockerfile
    assert "COPY skills/ /opt/oc-aicc-skills/" in dockerfile


def test_customer_answer_skill_does_not_document_legacy_write_execution() -> None:
    # 知识答复 Skill 只能调用 Task 2 提供的只读搜索工具，不能重新引入旧 CLI 写入或代码执行路径。
    body = (Path(__file__).resolve().parent.parent / "skills" / "aicc-customer-answer" / "SKILL.md").read_text(
        encoding="utf-8",
    )
    assert "oc-kb add" not in body
    assert "execute_code" not in body
    assert "aicc_knowledge_search" in body


def test_builtin_skills_declare_their_minimum_read_only_capabilities() -> None:
    # 三个内置 Skill 的声明必须恰好对应答复、公开检索与纯文本意向分析三类用途。
    root = Path(__file__).resolve().parent.parent / "skills"
    declared = {}
    for skill_md in root.glob("*/SKILL.md"):
        frontmatter = skill_md.read_text(encoding="utf-8")[4:].split("\n---\n", 1)[0]
        declared[skill_md.parent.name] = yaml.safe_load(frontmatter)["aicc_capabilities"]
    assert declared == {
        "aicc-customer-answer": ["knowledge.read"],
        "aicc-safe-web-research": ["web.search"],
        "aicc-lead-analysis": [],
    }


def test_sync_removes_previously_visible_generic_skill(tmp_path: Path) -> None:
    # 启动守卫必须清掉共享卷中旧镜像遗留的通用 Skill，Hermes 最终只能扫描客服白名单。
    source = tmp_path / "source"
    shutil.copytree(Path(__file__).resolve().parent.parent / "skills", source)
    generic = tmp_path / "data" / "skills" / "generic-terminal"
    generic.mkdir(parents=True)
    (generic / "SKILL.md").write_text("---\nname: generic-terminal\n---\n", encoding="utf-8")

    sync_aicc_builtin_skills(
        tmp_path / "data",
        frozenset({"knowledge.read", "web.search", "skills.read", "vision.read"}),
        source,
    )

    names = sorted(path.name for path in (tmp_path / "data" / "skills").iterdir() if path.is_dir())
    assert names == ["aicc-customer-answer", "aicc-lead-analysis", "aicc-safe-web-research"]
