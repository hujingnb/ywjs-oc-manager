#!/usr/local/lib/hermes-agent/venv/bin/python
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
    # qr_login 内部会 print 二维码 URL 到 stdout(同步阶段),之后进入 polling loop
    # 等用户扫码 + confirm(可能持续几分钟)。我们 redirect 整个 stdout 流到 stderr,
    # 让 qr_login 的所有 print 实时到 stderr;polling 结束后 stdout 恢复,json.dump
    # 写真正的 stdout。manager 端 docker stdcopy 分流:
    #   - stderr: 二维码 URL(实时,供前端展示)
    #   - stdout: 最终 JSON 凭证(qr_login 返回后才写)
    with contextlib.redirect_stdout(sys.stderr):
        cred = await qr_login("/opt/data", bot_type="3", timeout_seconds=480)

    if not cred:
        print("LOGIN_FAILED_OR_TIMEOUT", file=sys.stderr, flush=True)
        return 2

    json.dump(cred, sys.stdout)
    sys.stdout.write("\n")
    sys.stdout.flush()
    return 0


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
