"""验证 SOUL.md：平台层渲染、persona 拼接、旧知识库目录忽略、空层跳过。manifest v2 只渲染平台层。"""

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


def test_legacy_knowledge_files_are_ignored(tmp_input: Path, tmp_data: Path) -> None:
    # RAGFlow 接管知识库后，旧 input/resources/knowledge 文件不再 inline 到 SOUL.md。
    _setup(tmp_input, persona="P", platform="", org="", app="", kb_org={"big.md": "旧知识库内容"})
    render(make_manifest(), tmp_input, tmp_data)
    soul = (tmp_data / "SOUL.md").read_text()
    assert "旧知识库内容" not in soul
    assert "skills/kb-" not in soul


def _manifest_with_knowledge() -> Manifest:
    # 构造带 manifest.knowledge 配置的 Manifest，用于验证知识库指引渲染。
    return Manifest(
        app_id="x", app_name="X", app_model="m",
        openai_api_key="sk", openai_base_url="http://x",
        persona_rel="resources/persona.md",
        rule_platform_rel="resources/platform-rules.md",
        rule_organization_rel="resources/organization-rules.md",
        rule_application_rel="resources/application-rules.md",
        knowledge_runtime_base_url="http://manager-api:8080",
        knowledge_app_token="tok",
    )


def test_knowledge_guide_rendered_when_configured(tmp_input: Path, tmp_data: Path) -> None:
    # 配了 manifest.knowledge 时，SOUL.md 必须给出"强制优先知识库"的指引：
    # 既要包含 oc-kb search / add 两个命令，也要明确"先检索知识库、别用网络搜索代替"。
    _setup(tmp_input, persona="P body", platform="PLT")
    render(_manifest_with_knowledge(), tmp_input, tmp_data)
    soul = (tmp_data / "SOUL.md").read_text()
    assert "oc-kb search" in soul  # 检索主入口必须出现
    assert "oc-kb add" in soul  # 写入命令必须出现
    assert "知识库" in soul  # 指引以中文呈现
    # 强制优先语气：必须告知不要用网络搜索 / 记忆代替知识库检索
    assert "网络搜索" in soul


def test_knowledge_guide_absent_when_not_configured(tmp_input: Path, tmp_data: Path) -> None:
    # 未配 manifest.knowledge 时不得渲染知识库指引，避免误导模型调用不存在的 oc-kb skill。
    _setup(tmp_input, persona="P body", platform="PLT")
    render(make_manifest(), tmp_input, tmp_data)  # make_manifest 默认不含 knowledge
    soul = (tmp_data / "SOUL.md").read_text()
    assert "oc-kb" not in soul


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
