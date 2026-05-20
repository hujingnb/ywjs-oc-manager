"""验证 render_skills：扫 input/resources/knowledge/{org,app}/，slug 算法稳定。"""

import re
from pathlib import Path
from renderer.render_skills import render, slugify_knowledge_path


def test_slug_ascii(tmp_path) -> None:
    # 常规 ASCII 路径生成可读 slug。
    assert slugify_knowledge_path("policies/refund.md") == "policies-refund"
    assert slugify_knowledge_path("Tone.MD") == "tone"


def test_slug_non_ascii_falls_back_to_hash(tmp_path) -> None:
    # 含中文的路径回落到 kb-<sha256[:12]> 固定后缀。
    slug = slugify_knowledge_path("退款政策.md")
    assert re.match(r"^kb-[0-9a-f]{12}$", slug)


def test_render_creates_one_dir_per_file(tmp_input: Path, tmp_data: Path) -> None:
    # 每个 knowledge 文件生成一份 skills/kb-<scope>-<slug>/SKILL.md。
    (tmp_input / "resources" / "knowledge" / "org" / "policies").mkdir(parents=True)
    (tmp_input / "resources" / "knowledge" / "org" / "policies" / "refund.md").write_text("# Refund\n\nbody")
    (tmp_input / "resources" / "knowledge" / "app").mkdir(parents=True)
    (tmp_input / "resources" / "knowledge" / "app" / "tone.md").write_text("# Tone\n\nbody")

    outputs = render(tmp_input, tmp_data)

    assert (tmp_data / "skills" / "kb-org-policies-refund" / "SKILL.md").exists()
    assert (tmp_data / "skills" / "kb-app-tone" / "SKILL.md").exists()
    assert set(outputs) == {
        "skills/kb-org-policies-refund/SKILL.md",
        "skills/kb-app-tone/SKILL.md",
    }


def test_render_no_duplicate_h1_when_body_has_heading(tmp_input: Path, tmp_data: Path) -> None:
    # body 自带 H1 时，SKILL.md 正文不应再额外加一个标题（避免重复标题）。
    (tmp_input / "resources" / "knowledge" / "app").mkdir(parents=True)
    (tmp_input / "resources" / "knowledge" / "app" / "tone.md").write_text("# 话术\n\n正文")
    render(tmp_input, tmp_data)
    skill = (tmp_data / "skills" / "kb-app-tone" / "SKILL.md").read_text()
    # frontmatter 之后正文部分只应出现一次 "# 话术"。
    body_after_frontmatter = skill.split("---\n", 2)[-1]
    assert body_after_frontmatter.count("# 话术") == 1


def test_render_adds_h1_when_body_has_no_heading(tmp_input: Path, tmp_data: Path) -> None:
    # body 无 H1 时，用相对路径补一个标题，保证 SKILL.md 正文有抬头。
    (tmp_input / "resources" / "knowledge" / "app").mkdir(parents=True)
    (tmp_input / "resources" / "knowledge" / "app" / "plain.md").write_text("没有标题的纯正文")
    render(tmp_input, tmp_data)
    skill = (tmp_data / "skills" / "kb-app-plain" / "SKILL.md").read_text()
    assert "# plain.md" in skill
