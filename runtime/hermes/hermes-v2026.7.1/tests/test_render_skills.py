"""验证 render_skills：渲染 oc-kb skill 并解压版本 skill tar/zip。"""

import io
import json
import tarfile
import zipfile
from pathlib import Path

import pytest

from lib.manifest import Manifest
from renderer.render_skills import render


def _manifest(
    skills: list[str] | None = None,
    knowledge: bool = False,
    web_publish: bool = False,
) -> Manifest:
    # 构造渲染 skill 所需的最小 Manifest；knowledge=True 时启用 oc-kb skill，
    # web_publish=True 时启用 oc-publish skill（runtime_base_url + app_token 都给齐）。
    return Manifest(
        app_id="a", app_name="A", app_model="m",
        openai_api_key="sk", openai_base_url="http://x",
        persona_rel="resources/persona.md",
        rule_platform_rel="resources/platform-rules.md",
        skills=skills or [],
        knowledge_runtime_base_url="http://manager-api:8080" if knowledge else "",
        knowledge_app_token="runtime-token" if knowledge else "",
        web_publish_runtime_base_url="http://manager-api:8080" if web_publish else "",
        web_publish_app_token="publish-token" if web_publish else "",
    )


def _make_skill_tar(path: Path, skill_name: str) -> None:
    # 在 path 写一个扁平 skill tar：SKILL.md 直接位于归档顶层（与平台库上传/oc-ops 扁平契约一致）。
    path.parent.mkdir(parents=True, exist_ok=True)
    body = f"---\nname: {skill_name}\ndescription: d\n---\n# {skill_name}\n正文".encode()
    with tarfile.open(path, "w") as tw:
        info = tarfile.TarInfo("SKILL.md")
        info.size = len(body)
        tw.addfile(info, io.BytesIO(body))


def test_render_creates_runtime_knowledge_skill(tmp_input: Path, tmp_data: Path) -> None:
    # manifest.knowledge 存在时生成固定 oc-kb skill，但不把 app token 写入 SKILL.md。
    outputs = render(_manifest(knowledge=True), tmp_input, tmp_data)

    skill_path = tmp_data / "skills" / "oc-kb" / "SKILL.md"
    assert skill_path.exists()
    body = skill_path.read_text()
    assert "oc-kb search" in body
    assert "oc-kb add" in body
    assert "subprocess.run" in body
    assert "execute_code" in body
    assert "runtime-token" not in body
    assert outputs == ["skills/oc-kb/SKILL.md"]
    marker = tmp_data / "skills" / "oc-kb" / ".oc-managed"
    assert json.loads(marker.read_text())["source"] == "runtime-knowledge"


def test_render_runtime_knowledge_skill_mentions_industry_scope(tmp_input: Path, tmp_data: Path) -> None:
    # manifest.knowledge 存在时，oc-kb skill 说明必须提到行业知识库和 industry scope，避免模型忽略行业来源。
    outputs = render(_manifest(knowledge=True), tmp_input, tmp_data)

    body = (tmp_data / "skills" / "oc-kb" / "SKILL.md").read_text()
    assert "industry knowledge" in body
    assert 'scope="industry"' in body
    assert outputs == ["skills/oc-kb/SKILL.md"]


def test_render_skips_runtime_knowledge_skill_without_manifest_config(tmp_input: Path, tmp_data: Path) -> None:
    # manifest 没有 knowledge 配置时不生成 oc-kb，避免没有 token 的 skill 误导 Hermes。
    outputs = render(_manifest(), tmp_input, tmp_data)

    assert outputs == []
    assert not (tmp_data / "skills" / "oc-kb").exists()


def test_renders_oc_publish_when_web_publish_present(tmp_input: Path, tmp_data: Path) -> None:
    # manifest.web_publish 配置齐全（runtime_base_url + app_token）时生成固定 oc-publish skill：
    # 写出 SKILL.md（frontmatter name: oc-publish），打 web-publish 标记，但 app token 不写入正文。
    outputs = render(_manifest(web_publish=True), tmp_input, tmp_data)

    skill_path = tmp_data / "skills" / "oc-publish" / "SKILL.md"
    assert skill_path.exists()
    body = skill_path.read_text()
    assert "name: oc-publish" in body
    assert "publish-token" not in body
    assert "skills/oc-publish/SKILL.md" in outputs
    marker = tmp_data / "skills" / "oc-publish" / ".oc-managed"
    assert json.loads(marker.read_text())["source"] == "web-publish"


def test_skips_oc_publish_when_web_publish_absent(tmp_input: Path, tmp_data: Path) -> None:
    # 条件注入门控的核心安全特性：manifest 无 web_publish 段时整体不渲染 oc-publish，
    # 确保发布能力"不对所有人开放"——企业未开通时 Hermes 无从知晓也无法触发发布。
    outputs = render(_manifest(), tmp_input, tmp_data)

    assert "skills/oc-publish/SKILL.md" not in outputs
    assert not (tmp_data / "skills" / "oc-publish").exists()


def test_render_extracts_version_skill_tar(tmp_input: Path, tmp_data: Path) -> None:
    # manifest.skills 列出的扁平 tar 被解压到 data_root/skills/<归档文件名>/ 下，且目录含 .oc-managed 标记。
    _make_skill_tar(tmp_input / "resources" / "skills" / "weather.tar", "weather")
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
    tar_path = tmp_input / "resources" / "skills" / "evil-link.tar"
    tar_path.parent.mkdir(parents=True, exist_ok=True)
    with tarfile.open(tar_path, "w") as tw:
        link = tarfile.TarInfo("skill/escape")
        link.type = tarfile.SYMTYPE
        link.linkname = "../../../../tmp"
        tw.addfile(link)
    with pytest.raises(Exception):
        render(_manifest(["resources/skills/evil-link.tar"]), tmp_input, tmp_data)


def _make_skill_zip(zip_path: Path, skill_name: str) -> None:
    # 在 zip_path 写一个扁平 skill zip：SKILL.md 直接位于归档顶层。
    zip_path.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(zip_path, "w") as zf:
        zf.writestr("SKILL.md", f"---\nname: {skill_name}\n---\n")


def test_extract_zip_skill(tmp_input: Path, tmp_data: Path) -> None:
    # zip 格式扁平版本 skill 解压到 skills/<归档文件名>/ 并打 .oc-managed 标记。
    _make_skill_zip(tmp_input / "resources" / "skills" / "weather.zip", "weather")
    render(_manifest(["resources/skills/weather.zip"]), tmp_input, tmp_data)
    assert (tmp_data / "skills" / "weather" / "SKILL.md").exists()
    assert (tmp_data / "skills" / "weather" / ".oc-managed").exists()


def test_zip_rejects_traversal(tmp_input: Path, tmp_data: Path) -> None:
    # 含 ../ 越界条目的 zip 被拒（zip-slip 防护）。
    zip_path = tmp_input / "resources" / "skills" / "evil.zip"
    zip_path.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(zip_path, "w") as zf:
        zf.writestr("../evil/SKILL.md", "x")
    with pytest.raises(Exception):
        render(_manifest(["resources/skills/evil.zip"]), tmp_input, tmp_data)
