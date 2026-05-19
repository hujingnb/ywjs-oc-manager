#!/usr/bin/env python3
"""查询渠道绑定状态。stdout 单行 JSON。

调用形式：oc-channel-status --channel weixin
读取 hermes 自管的账号目录（/opt/data/weixin/accounts/），存在条目即视为
已绑定；不解析具体凭证字段，凭证由 hermes 自行使用。

输出协议：
- stdout 单行 JSON：
  - 已绑定：{"channel":"weixin","bound":true,"account_id":"<dir>"}
  - 未绑定：{"channel":"weixin","bound":false}
- 退出码：0 表示查询成功（不论是否绑定）；1 表示未知 channel。
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path


def main() -> int:
    # 命令行参数解析：仅 --channel；后续按需扩展。
    p = argparse.ArgumentParser()
    p.add_argument("--channel", required=True)
    args = p.parse_args()

    # OC_DATA_DIR 允许测试覆盖默认 /opt/data，方便本地 smoke。
    data_root = Path(os.environ.get("OC_DATA_DIR", "/opt/data"))

    if args.channel == "weixin":
        return _weixin_status(data_root)
    # 未知 channel：仍返回 JSON，但退出码 1 让 manager 知道不该期待此 channel。
    sys.stdout.write(json.dumps({"channel": args.channel, "bound": False, "reason": "unknown channel"}) + "\n")
    return 1


def _weixin_status(data_root: Path) -> int:
    """读取 hermes weixin 账号目录，判定是否已绑定。"""
    accounts_dir = data_root / "weixin" / "accounts"
    if not accounts_dir.exists():
        # 目录不存在：从未绑定或已解绑。
        sys.stdout.write(json.dumps({"channel": "weixin", "bound": False}) + "\n")
        return 0
    # 读 hermes 自管的账号目录里第一个有效条目作为绑定状态。
    # 当前版本只支持单账号绑定，因此遇到第一个目录即返回。
    for entry in accounts_dir.iterdir():
        if entry.is_dir():
            sys.stdout.write(json.dumps({"channel": "weixin", "bound": True, "account_id": entry.name}) + "\n")
            return 0
    # 目录存在但无子目录：视为未绑定。
    sys.stdout.write(json.dumps({"channel": "weixin", "bound": False}) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
