#!/usr/bin/env python3
"""解绑渠道。删除 hermes 自管的账号目录，触发 hermes 重新读 platforms 配置。

命名：oc- 前缀取自项目名 oc-manager，标识注入 hermes runtime 镜像、供容器内调用的运维 CLI
（区别于 hermes 上游自带命令）；后缀 channel-unbind = 渠道解绑。

调用形式：oc-channel-unbind --channel weixin
直接 rmtree /opt/data/weixin/accounts/，让 hermes 下次启动/重载 platforms
配置时识别为未绑定状态。

输出协议：
- stdout 单行 JSON：
  - 成功：{"status":"unbound"}
  - 未知 channel：{"status":"failed","reason":"unknown channel"}
- 退出码：0=unbound；1=未知 channel 或其他失败。
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import sys
from pathlib import Path


def main() -> int:
    # 命令行参数解析：仅 --channel。
    p = argparse.ArgumentParser()
    p.add_argument("--channel", required=True)
    args = p.parse_args()

    if args.channel != "weixin":
        # 当前只支持 weixin；其他 channel 一律 failed。
        sys.stdout.write(json.dumps({"status": "failed", "reason": "unknown channel"}) + "\n")
        return 1

    # OC_DATA_DIR 允许测试覆盖默认 /opt/data，方便本地 smoke。
    data_root = Path(os.environ.get("OC_DATA_DIR", "/opt/data"))
    accounts_dir = data_root / "weixin" / "accounts"
    if accounts_dir.exists():
        # 整体删除账号目录；hermes 下次扫描会判定为未绑定状态。
        shutil.rmtree(accounts_dir)
    # 即使目录原本不存在，也视为已解绑（幂等语义）。
    sys.stdout.write(json.dumps({"status": "unbound"}) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
