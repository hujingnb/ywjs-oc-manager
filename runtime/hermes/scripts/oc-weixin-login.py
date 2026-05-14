#!/usr/bin/env python3
"""manager docker exec 调用的微信扫码登录入口。

stdout:  单行 JSON,含 account_id/token/base_url/user_id 等凭证字段
stderr:  二维码 URL(供 manager 流式转发给前端展示)
exit 0:  登录成功
exit 2:  登录失败或超时
"""
import asyncio
import contextlib
import io
import json
import sys

from gateway.platforms.weixin import qr_login


async def main() -> int:
    # qr_login 内部会 print 二维码 URL 到 stdout。我们把整段 qr_login 输出捕获到
    # 缓冲区,从中提取 URL 写 stderr,保证 manager 端 stdout 只有最终 JSON。
    captured = io.StringIO()
    with contextlib.redirect_stdout(captured):
        cred = await qr_login("/opt/data", bot_type="3", timeout_seconds=480)

    for line in captured.getvalue().splitlines():
        stripped = line.strip()
        if stripped.startswith("https://liteapp.weixin.qq.com/"):
            print(stripped, file=sys.stderr, flush=True)

    if not cred:
        print("LOGIN_FAILED_OR_TIMEOUT", file=sys.stderr, flush=True)
        return 2

    json.dump(cred, sys.stdout)
    sys.stdout.write("\n")
    sys.stdout.flush()
    return 0


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
