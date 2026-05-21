"""验证 .env 渲染：行为开关 + 微信账号凭证转译。"""

import json
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


def test_render_env_no_weixin_when_unbound(tmp_data: Path) -> None:
    # 未扫码绑定（无 accounts 目录）时，.env 不应出现 WEIXIN_TOKEN /
    # WEIXIN_ACCOUNT_ID —— hermes 启动为纯 cron 模式。
    render(tmp_data)
    body = (tmp_data / ".env").read_text()
    assert "WEIXIN_TOKEN" not in body
    assert "WEIXIN_ACCOUNT_ID" not in body


def test_render_env_translates_weixin_account(tmp_data: Path) -> None:
    # accounts/<id>.json 存在时，把 hermes 自管凭证转译成 WEIXIN_* env，
    # 让 hermes gateway 启动时据此启用 weixin 平台。
    acc = tmp_data / "weixin" / "accounts"
    acc.mkdir(parents=True)
    (acc / "6a1277825225@im.bot.json").write_text(json.dumps({
        "token": "tok-xyz",
        "base_url": "https://weixin.example.com",
        "user_id": "u-1",
    }))
    render(tmp_data)
    body = (tmp_data / ".env").read_text()
    assert "WEIXIN_ACCOUNT_ID=6a1277825225@im.bot" in body
    assert "WEIXIN_TOKEN=tok-xyz" in body
    assert "WEIXIN_BASE_URL=https://weixin.example.com" in body


def test_render_env_weixin_account_without_base_url(tmp_data: Path) -> None:
    # base_url 缺失时只写 ACCOUNT_ID + TOKEN，不写空的 WEIXIN_BASE_URL 行。
    acc = tmp_data / "weixin" / "accounts"
    acc.mkdir(parents=True)
    (acc / "abc@im.bot.json").write_text(json.dumps({"token": "t1"}))
    render(tmp_data)
    body = (tmp_data / ".env").read_text()
    assert "WEIXIN_TOKEN=t1" in body
    assert "WEIXIN_BASE_URL" not in body


def test_render_env_skips_corrupt_account_file(tmp_data: Path) -> None:
    # 账号文件损坏（非法 JSON）不应阻断渲染，只是不产出 WEIXIN_* 段。
    acc = tmp_data / "weixin" / "accounts"
    acc.mkdir(parents=True)
    (acc / "broken@im.bot.json").write_text("{not json")
    render(tmp_data)
    body = (tmp_data / ".env").read_text()
    assert "GATEWAY_ALLOW_ALL_USERS=true" in body
    assert "WEIXIN_TOKEN" not in body
