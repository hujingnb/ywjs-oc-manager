"""验证 .env 仅写「行为开关」，不写 OPENAI_*（后者已进 config.yaml）。"""

from pathlib import Path
from renderer.render_env import render


def test_render_env_writes_behavior_flags(tmp_data: Path) -> None:
    # 验证 .env 含 GATEWAY_ALLOW_ALL_USERS 与 WEIXIN_DM_POLICY；
    # 不应再有 OPENAI_API_KEY / OPENAI_BASE_URL（这些在 config.yaml）。
    render(tmp_data)
    body = (tmp_data / ".env").read_text()
    assert "GATEWAY_ALLOW_ALL_USERS=true" in body
    assert "WEIXIN_DM_POLICY=open" in body
    assert "OPENAI_API_KEY" not in body
    assert "OPENAI_BASE_URL" not in body
