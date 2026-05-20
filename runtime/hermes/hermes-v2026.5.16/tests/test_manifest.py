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
