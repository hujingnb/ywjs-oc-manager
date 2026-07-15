"""验证 AICC 不在启动时注入可变 Skill。"""

from pathlib import Path

from lib.manifest import Manifest
from renderer.render_skills import render


def _manifest() -> Manifest:
    """构造即使携带旧 bootstrap 配置也必须被忽略的最小 manifest。"""
    return Manifest(
        app_id="a", app_name="A", app_model="m",
        openai_api_key="sk", openai_base_url="http://x",
        persona_rel="resources/persona.md", rule_platform_rel="resources/platform-rules.md",
        skills=["resources/skills/untrusted.tar"],
        knowledge_runtime_base_url="http://manager-api:8080", knowledge_app_token="runtime-token",
        web_publish_runtime_base_url="http://manager-api:8080", web_publish_app_token="publish-token",
    )


def test_render_skips_bootstrap_skills_and_runtime_write_skills(tmp_input: Path, tmp_data: Path) -> None:
    # AICC 的客服 Skill 仅在镜像构建时固定注入；启动时不能解压任意归档或生成 oc-kb/oc-publish。
    assert render(_manifest(), tmp_input, tmp_data) == []
    assert not (tmp_data / "skills" / "oc-kb").exists()
    assert not (tmp_data / "skills" / "oc-publish").exists()
    assert not (tmp_data / "skills" / "untrusted").exists()


def test_render_removes_stale_managed_skill_but_preserves_image_skill(tmp_input: Path, tmp_data: Path) -> None:
    # 重启时只能清理历史 managed 目录，镜像内置的只读客服 Skill 不得被 renderer 删除。
    stale = tmp_data / "skills" / "old-managed"
    stale.mkdir(parents=True)
    (stale / ".oc-managed").write_text("{}", encoding="utf-8")
    builtin = tmp_data / "skills" / "aicc-customer-answer"
    builtin.mkdir(parents=True)
    (builtin / "SKILL.md").write_text("---\nname: aicc-customer-answer\n---\n", encoding="utf-8")

    render(_manifest(), tmp_input, tmp_data)

    assert not stale.exists()
    assert (builtin / "SKILL.md").exists()
