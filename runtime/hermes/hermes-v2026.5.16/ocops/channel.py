# ocops/channel.py（先实现 status/unbind 同步部分；login 在 Task 6 追加）
"""渠道绑定运维：weixin 账号目录读写。从 oc-channel-status/unbind 抽取核心逻辑。

设计约束：manager 不解析凭证字段；status 仅判定是否存在账号文件，unbind 直接删目录
（幂等），凭证由 hermes 自管。未知 channel 一律 BAD_REQUEST（HTTP 400）。"""
from __future__ import annotations

import shutil
from pathlib import Path

from ocops.errors import OpsError


def channel_status(channel: str, data_root: Path) -> dict:
    """查询渠道绑定态；当前仅支持 weixin，未知 channel 抛 BAD_REQUEST。

    返回结构：
      - 未绑定：{"channel": "weixin", "bound": False}
      - 已绑定：{"channel": "weixin", "bound": True, "account_id": "<id>"}
    account_id 取 accounts/<id>.json 文件名去掉 .json 后缀；
    同一目录下只取第一个（当前单账号绑定语义）。
    """
    if channel != "weixin":
        raise OpsError("BAD_REQUEST", f"unknown channel: {channel}")
    accounts_dir = data_root / "weixin" / "accounts"
    if not accounts_dir.exists():
        # 目录不存在：从未绑定或已解绑。
        return {"channel": "weixin", "bound": False}
    for entry in sorted(accounts_dir.iterdir()):
        if entry.is_file() and entry.suffix == ".json":
            # 当前只支持单账号绑定，遇到第一个账号文件即返回。
            return {"channel": "weixin", "bound": True,
                    "account_id": entry.name[: -len(".json")]}
    # 目录存在但无账号文件：视为未绑定。
    return {"channel": "weixin", "bound": False}


def channel_unbind(channel: str, data_root: Path) -> dict:
    """解绑渠道：删除账号目录（幂等）；未知 channel 抛 BAD_REQUEST。

    直接删除 <data_root>/weixin/accounts/ 目录树，hermes 下次扫描
    platforms 配置时识别为未绑定状态。目录不存在时也视为已解绑（幂等语义）。
    """
    if channel != "weixin":
        raise OpsError("BAD_REQUEST", "unknown channel")
    accounts_dir = data_root / "weixin" / "accounts"
    if accounts_dir.exists():
        # 整体删除账号目录；hermes 下次扫描会判定为未绑定状态。
        shutil.rmtree(accounts_dir)
    return {"status": "unbound"}
