"""验证客服专用 config.yaml 的模型配置与最小权限运行时边界。"""

from pathlib import Path
import yaml
from lib.manifest import Manifest
from renderer.render_config_yaml import render


def make_manifest(routing: dict | None = None, app_language: str = "") -> Manifest:
    # 构造一个最小合法 Manifest 给渲染用；routing 缺省空（全部走主模型），
    # app_language 缺省空串（渲染时回落 "en"）。
    return Manifest(
        app_id="app-x", app_name="X", app_model="claude-3.7-sonnet",
        openai_api_key="sk-test",
        openai_base_url="http://new-api:3000",
        persona_rel="resources/persona.md",
        rule_platform_rel="resources/platform-rules.md",
        routing=routing or {},
        app_language=app_language,
    )


def test_render_writes_expected_fields(tmp_data: Path) -> None:
    # 客服配置应保留模型连接信息，但不应向 Hermes 注册通用终端。
    render(make_manifest(), tmp_data)
    out = yaml.safe_load((tmp_data / "config.yaml").read_text())
    assert out["model"]["default"] == "claude-3.7-sonnet"
    assert out["model"]["provider"] == "custom"
    assert out["model"]["base_url"] == "http://new-api:3000/v1"
    assert out["model"]["api_key"] == "sk-test"
    assert "terminal" not in out


def test_render_disables_cross_session_memory_and_user_profile(tmp_data: Path) -> None:
    # AICC 的跨会话上下文以 manager 为唯一真相源，避免 Hermes 长期记忆串访客。
    render(make_manifest(), tmp_data)
    out = yaml.safe_load((tmp_data / "config.yaml").read_text())
    assert out["memory"]["memory_enabled"] is False
    assert out["memory"]["user_profile_enabled"] is False


def test_render_auxiliary_all_main_when_routing_empty(tmp_data: Path) -> None:
    # routing 为空时，8 个 auxiliary 槽位全部 { provider: main }。
    render(make_manifest(), tmp_data)
    out = yaml.safe_load((tmp_data / "config.yaml").read_text())
    slots = ["vision", "compression", "web_extract", "session_search",
             "title_generation", "approval", "skills_hub", "mcp"]
    for slot in slots:
        assert out["auxiliary"][slot] == {"provider": "main"}, slot


def test_render_auxiliary_uses_routing_model(tmp_data: Path) -> None:
    # routing 指定了某槽位模型时，该槽位渲染为 custom + 该模型 + 凭证；其余仍走 main。
    render(make_manifest({"vision": "gpt-5.4", "title_generation": "qwen3.5:27b"}), tmp_data)
    out = yaml.safe_load((tmp_data / "config.yaml").read_text())
    assert out["auxiliary"]["vision"] == {
        "provider": "custom", "model": "gpt-5.4",
        "base_url": "http://new-api:3000/v1", "api_key": "sk-test",
    }
    assert out["auxiliary"]["title_generation"]["model"] == "qwen3.5:27b"
    assert out["auxiliary"]["compression"] == {"provider": "main"}


def test_render_is_atomic(tmp_data: Path) -> None:
    # 渲染完成后不应留 .tmp 残留。
    render(make_manifest(), tmp_data)
    leftovers = [p.name for p in tmp_data.iterdir() if p.suffix == ".tmp"]
    assert leftovers == []


def test_render_display_language_defaults_en(tmp_data: Path) -> None:
    # manifest 未指定 app.language（空串）时，display.language 应回落 "en"。
    # hermes i18n 据此把所有走 t() 的文案输出为英文。
    render(make_manifest(app_language=""), tmp_data)
    out = yaml.safe_load((tmp_data / "config.yaml").read_text())
    assert out["display"]["language"] == "en"


def test_render_display_language_zh(tmp_data: Path) -> None:
    # manifest app.language="zh" 时，display.language 应为 "zh"，
    # hermes i18n 输出简体中文文案。
    render(make_manifest(app_language="zh"), tmp_data)
    out = yaml.safe_load((tmp_data / "config.yaml").read_text())
    assert out["display"]["language"] == "zh"


def test_render_display_language_en(tmp_data: Path) -> None:
    # manifest app.language="en" 时，display.language 应为 "en"，
    # hermes i18n 输出英文文案。
    render(make_manifest(app_language="en"), tmp_data)
    out = yaml.safe_load((tmp_data / "config.yaml").read_text())
    assert out["display"]["language"] == "en"


def test_render_omits_command_approvals(tmp_data: Path) -> None:
    # 客服镜像没有命令工具，不能携带跳过危险命令审批的 approvals 配置。
    render(make_manifest(), tmp_data)
    out = yaml.safe_load((tmp_data / "config.yaml").read_text())
    assert "approvals" not in out
