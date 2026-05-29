#!/usr/bin/env python3
"""查询渠道绑定状态。stdout 单行 JSON。

命名：oc- 前缀取自项目名 oc-manager，标识注入 hermes runtime 镜像、供容器内调用的运维 CLI
（区别于 hermes 上游自带命令）；后缀 channel-status = 渠道绑定状态查询。

调用形式：oc-channel-status --channel weixin
读取 hermes 自管的账号目录（/opt/data/weixin/accounts/），存在账号文件即视为
已绑定；不解析具体凭证字段，凭证由 hermes 自行使用。

hermes 上游把每个微信账号存为 accounts/<account_id>.json 文件
（account_id 形如 <hex>@im.bot），不是子目录。account_id 取文件名去掉
.json 后缀。

输出协议（CLI shim 保留原对外契约）：
- stdout 单行 JSON：
  - 已绑定：{"channel":"weixin","bound":true,"account_id":"<id>"}
  - 未绑定：{"channel":"weixin","bound":false}
  - 未知 channel：{"channel":"<ch>","bound":false,"reason":"unknown channel"}
- 退出码：0 表示查询成功（不论是否绑定）；1 表示未知 channel。

本脚本是 CLI shim：核心逻辑已下沉至 ocops.channel.channel_status，
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

    from ocops.channel import channel_status
    from ocops.errors import OpsError

    # 命令行参数解析：仅 --channel；后续按需扩展。
    p = argparse.ArgumentParser()
    p.add_argument("--channel", required=True)
    args = p.parse_args()

    # OC_DATA_DIR 允许测试覆盖默认 /opt/data，方便本地 smoke。
    data_root = Path(os.environ.get("OC_DATA_DIR", "/opt/data"))

    try:
        result = channel_status(args.channel, data_root)
        sys.stdout.write(json.dumps(result, ensure_ascii=False) + "\n")
        return 0
    except OpsError as e:
        # 未知 channel：翻译回原输出形态（带 reason 字段）且退出码 1，
        # 保证对外命令契约不变（manager 通过退出码判定未知 channel）。
        sys.stdout.write(json.dumps(
            {"channel": args.channel, "bound": False, "reason": "unknown channel"},
            ensure_ascii=False,
        ) + "\n")
        return 1


if __name__ == "__main__":
    sys.exit(main())
