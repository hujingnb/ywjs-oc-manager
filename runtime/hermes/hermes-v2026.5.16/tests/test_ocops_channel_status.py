"""覆盖渠道 status/unbind 核心函数：基于 /opt/data/weixin/accounts 目录判定绑定态。"""
from ocops import channel
from ocops.errors import OpsError


def test_status_unbound_when_dir_absent(tmp_path):
    # 边界：账号目录不存在 → 未绑定
    got = channel.channel_status("weixin", tmp_path)
    assert got == {"channel": "weixin", "bound": False}


def test_status_bound_returns_account_id(tmp_path):
    # 正常路径：存在 <id>.json 账号文件 → 已绑定且回传 account_id
    accounts = tmp_path / "weixin" / "accounts"
    accounts.mkdir(parents=True)
    (accounts / "abc@im.bot.json").write_text("{}")
    got = channel.channel_status("weixin", tmp_path)
    assert got == {"channel": "weixin", "bound": True, "account_id": "abc@im.bot"}


def test_status_unknown_channel_raises(tmp_path):
    # 异常路径：未知 channel → OpsError(BAD_REQUEST)
    try:
        channel.channel_status("telegram", tmp_path)
        assert False
    except OpsError as e:
        assert e.code == "BAD_REQUEST"


def test_unbind_idempotent(tmp_path):
    # 幂等：目录不存在也返回 unbound
    assert channel.channel_unbind("weixin", tmp_path) == {"status": "unbound"}
