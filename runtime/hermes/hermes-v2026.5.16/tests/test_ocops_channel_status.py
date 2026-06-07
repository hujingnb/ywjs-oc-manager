"""覆盖渠道 status/unbind 核心函数：基于 /opt/data/weixin/accounts 目录判定绑定态。"""
from jsonschema import validate

from ocops import channel
from ocops.errors import OpsError


def test_status_unbound_when_dir_absent(tmp_path, ocops_schema):
    # 边界：账号目录不存在 → 未绑定
    got = channel.channel_status("weixin", tmp_path)
    validate(got, ocops_schema("channel/status.schema.json"))
    assert got == {"channel": "weixin", "bound": False}


def test_status_bound_returns_account_id(tmp_path, ocops_schema):
    # 正常路径：存在 <id>.json 账号文件 → 已绑定且回传 account_id
    accounts = tmp_path / "weixin" / "accounts"
    accounts.mkdir(parents=True)
    (accounts / "abc@im.bot.json").write_text("{}")
    got = channel.channel_status("weixin", tmp_path)
    validate(got, ocops_schema("channel/status.schema.json"))
    assert got == {"channel": "weixin", "bound": True, "account_id": "abc@im.bot"}


def test_status_skips_sidecar_files(tmp_path):
    # 复现 bug：账号目录除 <id>.json 外，hermes 还会写 sidecar 文件
    # <id>.context-tokens.json / <id>.sync.json。旧实现 sorted(iterdir())[0] 会因
    # ".context-tokens" 字典序在前而把它误当账号，account_id 变成 "abc@im.bot.context-tokens"。
    # 修复后必须跳过 sidecar，只回真正的账号本体 id。
    accounts = tmp_path / "weixin" / "accounts"
    accounts.mkdir(parents=True)
    # 故意先放两个 sidecar（字典序均排在 <id>.json 之前），再放账号本体。
    (accounts / "abc@im.bot.context-tokens.json").write_text('{"u": "tok"}')
    (accounts / "abc@im.bot.sync.json").write_text('{"get_updates_buf": "x"}')
    (accounts / "abc@im.bot.json").write_text('{"token": "t", "base_url": "u"}')
    got = channel.channel_status("weixin", tmp_path)
    # account_id 必须是账号本体 id，不带 sidecar 后缀。
    assert got == {"channel": "weixin", "bound": True, "account_id": "abc@im.bot"}


def test_status_unbound_when_only_sidecars(tmp_path):
    # 边界：目录里只有 sidecar、没有账号本体 → 视为未绑定（不被 sidecar 误判为已绑定）。
    accounts = tmp_path / "weixin" / "accounts"
    accounts.mkdir(parents=True)
    (accounts / "abc@im.bot.sync.json").write_text('{"get_updates_buf": "x"}')
    got = channel.channel_status("weixin", tmp_path)
    assert got == {"channel": "weixin", "bound": False}


def test_status_unknown_channel_raises(tmp_path):
    # 异常路径：未知 channel → OpsError(BAD_REQUEST)
    try:
        channel.channel_status("telegram", tmp_path)
        assert False
    except OpsError as e:
        assert e.code == "BAD_REQUEST"


def test_unbind_idempotent(tmp_path, ocops_schema):
    # 幂等：目录不存在也返回 unbound
    got = channel.channel_unbind("weixin", tmp_path)
    validate(got, ocops_schema("channel/unbind.schema.json"))
    assert got == {"status": "unbound"}
