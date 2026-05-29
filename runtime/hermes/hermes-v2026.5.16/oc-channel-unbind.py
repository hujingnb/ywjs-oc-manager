#!/usr/bin/env python3
"""解绑渠道。删除 hermes 自管的账号目录，触发 hermes 重新读 platforms 配置。

命名：oc- 前缀取自项目名 oc-manager，标识注入 hermes runtime 镜像、供容器内调用的运维 CLI
（区别于 hermes 上游自带命令）；后缀 channel-unbind = 渠道解绑。

调用形式：oc-channel-unbind --channel weixin
直接 rmtree /opt/data/weixin/accounts/，让 hermes 下次启动/重载 platforms
配置时识别为未绑定状态。

输出协议（CLI shim 保留原对外契约）：
- stdout 单行 JSON：
  - 成功：{"status":"unbound"}
  - 未知 channel：{"status":"failed","reason":"unknown channel"}
- 退出码：0=unbound；1=未知 channel 或其他失败。

本脚本是 CLI shim：核心逻辑已下沉至 ocops.channel.channel_unbind，
OpsError(BAD_REQUEST) 在此翻译回原输出形态与退出码，保证对外命令不变。
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path


def main() -> int:
    # CLI shim：解析参数后调用 ocops.channel 核心逻辑，保留原对外输出契约。
    sys.path.insert(0, "/usr/local/lib")  # 镜像内 ocops 装在 /usr/local/lib/ocops
    sys.path.insert(0, str(Path(__file__).resolve().parent))  # 本地自检 fallback

    from ocops.channel import channel_unbind
    from ocops.errors import OpsError

    # 命令行参数解析：仅 --channel。
    p = argparse.ArgumentParser()
    p.add_argument("--channel", required=True)
    args = p.parse_args()

    # OC_DATA_DIR 允许测试覆盖默认 /opt/data，方便本地 smoke。
    data_root = Path(os.environ.get("OC_DATA_DIR", "/opt/data"))

    try:
        result = channel_unbind(args.channel, data_root)
        sys.stdout.write(json.dumps(result, ensure_ascii=False) + "\n")
        return 0
    except OpsError as e:
        # 未知 channel：翻译回原输出形态（status:failed + reason 字段）且退出码 1，
        # 保证对外命令契约不变（manager 通过退出码判定未知 channel）。
        sys.stdout.write(json.dumps(
            {"status": "failed", "reason": "unknown channel"},
            ensure_ascii=False,
        ) + "\n")
        return 1


if __name__ == "__main__":
    sys.exit(main())
