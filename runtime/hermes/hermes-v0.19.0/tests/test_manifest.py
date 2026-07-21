"""验证 manifest.load 校验必填字段、忽略未知字段。"""

from pathlib import Path
import pytest
from lib.manifest import load, ManifestError


def write(p: Path, body: str) -> Path:
    # 写一个 manifest.yaml 测试样本。
    p.write_text(body)
    return p


def test_load_minimal_ok(tmp_path: Path) -> None:
    # 验证最小合法 manifest 能解析出 app.model 与 openai 凭证。
    p = write(tmp_path / "manifest.yaml", """
app:
  id: app-x
  name: x
  model: claude-3.7-sonnet
credentials:
  openai:
    api_key: sk-x
    base_url: http://new-api:3000
resources:
  persona: resources/persona.md
  rules:
    platform: resources/platform-rules.md
    organization: resources/organization-rules.md
    application: resources/application-rules.md
""")
    m = load(p)
    assert m.app_model == "claude-3.7-sonnet"
    assert m.openai_api_key == "sk-x"
    assert m.openai_base_url == "http://new-api:3000"


def test_load_unknown_field_is_ignored(tmp_path: Path) -> None:
    # 验证未知顶层字段不会导致解析失败（forward-compat）。
    p = write(tmp_path / "manifest.yaml", """
app:
  id: app-x
  name: x
  model: m
credentials:
  openai: { api_key: sk-x, base_url: http://x }
resources:
  persona: resources/persona.md
  rules:
    platform: resources/platform-rules.md
    organization: resources/organization-rules.md
    application: resources/application-rules.md
future_field:
  whatever: 1
""")
    m = load(p)
    assert m.app_model == "m"


def test_load_missing_model_raises(tmp_path: Path) -> None:
    # 验证必填字段缺失抛 ManifestError。
    p = write(tmp_path / "manifest.yaml", """
app:
  id: app-x
  name: x
credentials:
  openai: { api_key: sk-x, base_url: http://x }
resources:
  persona: x.md
  rules: { platform: a, organization: b, application: c }
""")
    with pytest.raises(ManifestError):
        load(p)


def test_load_v2_parses_routing_and_skills(tmp_path: Path) -> None:
    # 验证 manifest v2 的 routing、resources.skills 与 knowledge 配置被解析；org/app rule 缺省也合法。
    p = write(tmp_path / "manifest.yaml", """
app:
  id: app-x
  name: x
  model: m
routing:
  vision: gpt-5.4
  compression: deepseek-flash
knowledge:
  runtime_base_url: http://manager-api:8080
  app_token: runtime-token
credentials:
  openai: { api_key: sk-x, base_url: http://x }
resources:
  persona: resources/persona.md
  rules:
    platform: resources/platform-rules.md
  skills:
    - resources/skills/weather.tar
    - resources/skills/calc.tar
""")
    m = load(p)
    assert m.routing == {"vision": "gpt-5.4", "compression": "deepseek-flash"}
    assert m.skills == ["resources/skills/weather.tar", "resources/skills/calc.tar"]
    assert m.knowledge_runtime_base_url == "http://manager-api:8080"
    assert m.knowledge_app_token == "runtime-token"
    assert m.rule_organization_rel == ""
    assert m.rule_application_rel == ""


def test_load_parses_web_publish(tmp_path: Path) -> None:
    # 验证 manifest.web_publish 段被解析到 Manifest 的三个 web_publish_* 字段。
    p = write(tmp_path / "manifest.yaml", """
app:
  id: app-x
  name: x
  model: m
web_publish:
  runtime_base_url: http://manager-api:8080
  app_token: publish-token
  base_domain: apps.example.com
credentials:
  openai: { api_key: sk-x, base_url: http://x }
resources:
  persona: resources/persona.md
  rules:
    platform: resources/platform-rules.md
""")
    m = load(p)
    assert m.web_publish_runtime_base_url == "http://manager-api:8080"
    assert m.web_publish_app_token == "publish-token"
    assert m.web_publish_base_domain == "apps.example.com"


def test_load_web_publish_absent_defaults_empty(tmp_path: Path) -> None:
    # 验证 manifest 不含 web_publish 段时三个字段缺省空串（条件注入门控：企业未开通即不渲染 oc-publish）。
    p = write(tmp_path / "manifest.yaml", """
app:
  id: app-x
  name: x
  model: m
credentials:
  openai: { api_key: sk-x, base_url: http://x }
resources:
  persona: resources/persona.md
  rules:
    platform: resources/platform-rules.md
""")
    m = load(p)
    assert m.web_publish_runtime_base_url == ""
    assert m.web_publish_app_token == ""
    assert m.web_publish_base_domain == ""


def test_load_v1_without_routing_skills_defaults_empty(tmp_path: Path) -> None:
    # 验证老 v1 manifest（无 routing / skills）解析后 routing={}、skills=[]，保持向后兼容。
    p = write(tmp_path / "manifest.yaml", """
app:
  id: app-x
  name: x
  model: m
credentials:
  openai: { api_key: sk-x, base_url: http://x }
resources:
  persona: resources/persona.md
  rules:
    platform: resources/platform-rules.md
    organization: resources/organization-rules.md
    application: resources/application-rules.md
""")
    m = load(p)
    assert m.routing == {}
    assert m.skills == []
    assert m.rule_platform_rel == "resources/platform-rules.md"


def test_load_app_language_zh(tmp_path: Path) -> None:
    # 验证 manifest app.language="zh" 被解析到 Manifest.app_language。
    p = write(tmp_path / "manifest.yaml", """
app:
  id: app-x
  name: x
  model: m
  language: zh
credentials:
  openai: { api_key: sk-x, base_url: http://x }
resources:
  persona: resources/persona.md
  rules:
    platform: resources/platform-rules.md
""")
    m = load(p)
    assert m.app_language == "zh"


def test_load_app_language_en(tmp_path: Path) -> None:
    # 验证 manifest app.language="en" 被解析到 Manifest.app_language。
    p = write(tmp_path / "manifest.yaml", """
app:
  id: app-x
  name: x
  model: m
  language: en
credentials:
  openai: { api_key: sk-x, base_url: http://x }
resources:
  persona: resources/persona.md
  rules:
    platform: resources/platform-rules.md
""")
    m = load(p)
    assert m.app_language == "en"


def test_load_app_language_absent_defaults_empty(tmp_path: Path) -> None:
    # 验证 manifest 不含 app.language 时，Manifest.app_language 缺省为空串（渲染侧回落 "en"）。
    p = write(tmp_path / "manifest.yaml", """
app:
  id: app-x
  name: x
  model: m
credentials:
  openai: { api_key: sk-x, base_url: http://x }
resources:
  persona: resources/persona.md
  rules:
    platform: resources/platform-rules.md
""")
    m = load(p)
    assert m.app_language == ""
