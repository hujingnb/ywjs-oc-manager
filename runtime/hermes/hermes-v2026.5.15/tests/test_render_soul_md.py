"""验证 SOUL.md：平台层渲染、persona 拼接、知识库 inline 截断、空层跳过。manifest v2 只渲染平台层。"""

from pathlib import Path
from lib.manifest import Manifest
from renderer.render_soul_md import render


def make_manifest() -> Manifest:
    # 构造满足必填字段的 Manifest；resources 相对路径与 _setup 内布局一致。
    return Manifest(
        app_id="x", app_name="X", app_model="m",
        openai_api_key="sk", openai_base_url="http://x",
        persona_rel="resources/persona.md",
        rule_platform_rel="resources/platform-rules.md",
        rule_organization_rel="resources/organization-rules.md",
        rule_application_rel="resources/application-rules.md",
    )


def _setup(input_root: Path, *, persona="", platform="", org="", app="", kb_org=None, kb_app=None) -> None:
    # 准备 input 目录下的 resources 文件；空字符串表示该层不存在。
    res = input_root / "resources"
    res.mkdir(parents=True, exist_ok=True)
    (res / "persona.md").write_text(persona)
    (res / "platform-rules.md").write_text(platform)
    (res / "organization-rules.md").write_text(org)
    (res / "application-rules.md").write_text(app)
    if kb_org:
        for rel, body in kb_org.items():
            f = res / "knowledge" / "org" / rel
            f.parent.mkdir(parents=True, exist_ok=True)
            f.write_text(body)
    if kb_app:
        for rel, body in kb_app.items():
            f = res / "knowledge" / "app" / rel
            f.parent.mkdir(parents=True, exist_ok=True)
            f.write_text(body)


def test_three_layers_in_order(tmp_input: Path, tmp_data: Path) -> None:
    # manifest v2：验证渲染顺序 persona → platform；组织层 / 应用层不再渲染。
    _setup(tmp_input, persona="P body", platform="PLT", org="ORG", app="APP")
    render(make_manifest(), tmp_input, tmp_data)
    soul = (tmp_data / "SOUL.md").read_text()
    assert soul.index("P body") < soul.index("PLT")
    assert "ORG" not in soul
    assert "APP" not in soul


def test_empty_layer_skipped(tmp_input: Path, tmp_data: Path) -> None:
    # 验证平台层为空时，平台层 section 不出现；组织层 / 应用层 manifest v2 起始终不渲染。
    _setup(tmp_input, persona="P", platform="", org="ORG", app="APP")
    render(make_manifest(), tmp_input, tmp_data)
    soul = (tmp_data / "SOUL.md").read_text()
    assert "平台层" not in soul
    assert "组织层" not in soul
    assert "应用层" not in soul


def test_knowledge_inline_truncated_at_8kib(tmp_input: Path, tmp_data: Path) -> None:
    # 验证单个知识库文件超过 8 KiB 被截断，并附带 "完整版见 skills/kb-*" 提示。
    big = "A" * 9000
    _setup(tmp_input, persona="P", platform="", org="", app="", kb_org={"big.md": big})
    render(make_manifest(), tmp_input, tmp_data)
    soul = (tmp_data / "SOUL.md").read_text()
    assert "AAAA" in soul
    assert "完整版见" in soul or "skills/kb-" in soul
    assert soul.count("A") < 9000


def test_render_drops_org_and_app_layers(tmp_input: Path, tmp_data: Path) -> None:
    # manifest v2：即使 input 里仍有组织层 / 应用层 rule 文件，SOUL.md 也只渲染平台层。
    res = tmp_input / "resources"
    res.mkdir(parents=True)
    (res / "persona.md").write_text("我是版本人设")
    (res / "platform-rules.md").write_text("平台规则正文")
    (res / "organization-rules.md").write_text("组织规则正文")
    (res / "application-rules.md").write_text("应用规则正文")
    m = Manifest(
        app_id="a", app_name="A", app_model="m",
        openai_api_key="sk", openai_base_url="http://x",
        persona_rel="resources/persona.md",
        rule_platform_rel="resources/platform-rules.md",
        rule_organization_rel="resources/organization-rules.md",
        rule_application_rel="resources/application-rules.md",
    )
    render(m, tmp_input, tmp_data)
    soul = (tmp_data / "SOUL.md").read_text()
    assert "## 平台层" in soul
    assert "平台规则正文" in soul
    assert "我是版本人设" in soul
    assert "## 组织层" not in soul
    assert "## 应用层" not in soul
    assert "组织规则正文" not in soul
    assert "应用规则正文" not in soul
