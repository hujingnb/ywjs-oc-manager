"""验证 render_skills：扫 input/resources/knowledge/{org,app}/，slug 算法稳定。"""

import io
import json
import re
import tarfile
from pathlib import Path

from lib.manifest import Manifest
from renderer.render_skills import render, slugify_knowledge_path


def _manifest(skills: list[str] | None = None) -> Manifest:
    # 构造渲染 skill 所需的最小 Manifest；skills 缺省空。
    return Manifest(
        app_id="a", app_name="A", app_model="m",
        openai_api_key="sk", openai_base_url="http://x",
        persona_rel="resources/persona.md",
        rule_platform_rel="resources/platform-rules.md",
        skills=skills or [],
    )


def _make_skill_tar(path: Path, skill_dir: str, skill_name: str) -> None:
    # 在 path 写一个 skill tar：内含 <skill_dir>/SKILL.md（带 frontmatter）。
    path.parent.mkdir(parents=True, exist_ok=True)
    body = f"---\nname: {skill_name}\ndescription: d\n---\n# {skill_name}\n正文".encode()
    with tarfile.open(path, "w") as tw:
        info = tarfile.TarInfo(f"{skill_dir}/SKILL.md")
        info.size = len(body)
        tw.addfile(info, io.BytesIO(body))


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

    outputs = render(_manifest(), tmp_input, tmp_data)

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
    render(_manifest(), tmp_input, tmp_data)
    skill = (tmp_data / "skills" / "kb-app-tone" / "SKILL.md").read_text()
    # frontmatter 之后正文部分只应出现一次 "# 话术"。
    body_after_frontmatter = skill.split("---\n", 2)[-1]
    assert body_after_frontmatter.count("# 话术") == 1


def test_render_adds_h1_when_body_has_no_heading(tmp_input: Path, tmp_data: Path) -> None:
    # body 无 H1 时，用相对路径补一个标题，保证 SKILL.md 正文有抬头。
    (tmp_input / "resources" / "knowledge" / "app").mkdir(parents=True)
    (tmp_input / "resources" / "knowledge" / "app" / "plain.md").write_text("没有标题的纯正文")
    render(_manifest(), tmp_input, tmp_data)
    skill = (tmp_data / "skills" / "kb-app-plain" / "SKILL.md").read_text()
    assert "# plain.md" in skill


def test_render_extracts_version_skill_tar(tmp_input: Path, tmp_data: Path) -> None:
    # manifest.skills 列出的 tar 被解压到 data_root/skills/ 下，且目录含 .oc-managed 标记。
    _make_skill_tar(tmp_input / "resources" / "skills" / "weather.tar", "weather", "weather")
    render(_manifest(["resources/skills/weather.tar"]), tmp_input, tmp_data)
    assert (tmp_data / "skills" / "weather" / "SKILL.md").exists()
    marker = tmp_data / "skills" / "weather" / ".oc-managed"
    assert marker.exists()
    assert json.loads(marker.read_text())["source"] == "version-skill"


def test_render_wipes_previously_managed_skill(tmp_input: Path, tmp_data: Path) -> None:
    # 上次安装的版本 skill（带 .oc-managed）在本次渲染前被清掉；不再出现在 manifest.skills 时即消失。
    stale = tmp_data / "skills" / "old-skill"
    stale.mkdir(parents=True)
    (stale / "SKILL.md").write_text("old")
    (stale / ".oc-managed").write_text('{"source":"version-skill"}')
    render(_manifest(), tmp_input, tmp_data)  # 本次 skills 为空
    assert not stale.exists()


def test_render_keeps_builtin_skill_without_marker(tmp_input: Path, tmp_data: Path) -> None:
    # 镜像内置 skill（无 .oc-managed）不被清理。
    builtin = tmp_data / "skills" / "builtin-skill"
    builtin.mkdir(parents=True)
    (builtin / "SKILL.md").write_text("builtin")
    render(_manifest(), tmp_input, tmp_data)
    assert (builtin / "SKILL.md").exists()


def test_render_rejects_unsafe_tar_path(tmp_input: Path, tmp_data: Path) -> None:
    # tar 含越界路径条目时抛错，不把文件写到 skills/ 之外。
    import pytest
    tar_path = tmp_input / "resources" / "skills" / "evil.tar"
    tar_path.parent.mkdir(parents=True, exist_ok=True)
    with tarfile.open(tar_path, "w") as tw:
        body = b"x"
        info = tarfile.TarInfo("../evil/SKILL.md")
        info.size = len(body)
        tw.addfile(info, io.BytesIO(body))
    with pytest.raises(Exception):
        render(_manifest(["resources/skills/evil.tar"]), tmp_input, tmp_data)


def test_render_rejects_tar_symlink_escape(tmp_input: Path, tmp_data: Path) -> None:
    # tar 含指向解压目录之外的符号链接时，extractall(filter="data") 拒绝，不写出界文件。
    import pytest
    tar_path = tmp_input / "resources" / "skills" / "evil-link.tar"
    tar_path.parent.mkdir(parents=True, exist_ok=True)
    with tarfile.open(tar_path, "w") as tw:
        link = tarfile.TarInfo("skill/escape")
        link.type = tarfile.SYMTYPE
        link.linkname = "../../../../tmp"
        tw.addfile(link)
    with pytest.raises(Exception):
        render(_manifest(["resources/skills/evil-link.tar"]), tmp_input, tmp_data)
