#!/usr/bin/env python3
"""触发渠道绑定。stdout 结束态 JSON；stderr 中间事件 JSON（含二维码 URL）。

调用形式：oc-channel-login --channel weixin
按 --channel 分发到具体实现；首版仅支持 weixin。

输出协议与 spec §7 一致：
- stdout 单行 JSON：{"status":"bound"|"failed"|"timeout","reason":"..."}
- stderr 中间事件 JSON（每行一条）：{"event":"qrcode","url":"..."} 等
- 退出码：0=bound；1=其他
"""

from __future__ import annotations

import argparse
import asyncio
import contextlib
import json
import sys


def main() -> int:
    # 命令行参数解析：当前只识别 --channel，后续可扩展更多渠道分支。
    p = argparse.ArgumentParser()
    p.add_argument("--channel", required=True)
    args = p.parse_args()

    if args.channel == "weixin":
        return asyncio.run(_weixin_login())
    # 未知 channel：以 failed 状态返回，避免 manager 端阻塞。
    sys.stdout.write(json.dumps({"status": "failed", "reason": f"unknown channel: {args.channel}"}) + "\n")
    return 1


async def _weixin_login() -> int:
    """微信扫码登录。

    调用 hermes 上游 gateway.platforms.weixin.qr_login，传入 /opt/data
    作为凭证落盘根。qr_login 内部会：
    - print 二维码 URL（同步阶段）到 stdout —— 我们 redirect 到 stderr
    - 进入 polling loop 等用户扫码 + confirm（可达 8 分钟）
    - 成功返回 cred dict + 自动把账号目录写到 /opt/data/weixin/accounts/
    - 失败/超时返回 None

    新方案下 manager 不再解析 cred，凭证完全由 hermes 自管；本脚本 stdout
    只输出最终态 {"status":"bound"|"failed"|"timeout"}。
    """
    try:
        # 上游 SDK 仅在 hermes 容器内安装；本地开发/CI 环境通常不可用。
        from gateway.platforms.weixin import qr_login
    except ImportError as e:
        # 本地开发/测试环境没装 hermes 上游 SDK；退化为 failed 并给出原因。
        sys.stdout.write(json.dumps({"status": "failed", "reason": f"hermes SDK not available: {e}"}) + "\n")
        return 1

    # qr_login 内部 print 二维码 URL 到 stdout，redirect 到 stderr 让 manager
    # 可以流式获取。stderr 文本不是 JSON 包装的（依赖上游格式），manager 端
    # 仍可按行抓取并转发给前端。
    with contextlib.redirect_stdout(sys.stderr):
        cred = await qr_login("/opt/data", bot_type="3", timeout_seconds=480)

    if not cred:
        # 上游返回 None 表示失败或超时；当前协议统一标记为 timeout，
        # manager 端按非 bound 处理。
        sys.stdout.write(json.dumps({"status": "timeout"}) + "\n")
        return 1

    # 成功：凭证已由 qr_login 自动落盘到 /opt/data/weixin/accounts/。
    sys.stdout.write(json.dumps({"status": "bound"}) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
