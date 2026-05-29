#!/usr/bin/env python3
"""触发渠道绑定。stdout 结束态 JSON；stderr 中间事件 JSON（含二维码 URL）。

命名：oc- 前缀取自项目名 oc-manager，标识注入 hermes runtime 镜像、供容器内调用的运维 CLI
（区别于 hermes 上游自带命令）；后缀 channel-login = 渠道登录 / 绑定。

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
import json
import os
import sys
from pathlib import Path

# hermes 上游 SDK (gateway.platforms.weixin 等) 装在镜像 install.sh 创建的
# uv venv 里 (/usr/local/lib/hermes-agent/venv)，系统 Python 看不到。
# 这里检测到 venv 存在且当前进程不是它时, 直接 re-exec 让脚本在 venv 里跑,
# 让后续 `from gateway.platforms.weixin import qr_login` 能正常 import。
# venv 不存在 (例如 stub 镜像) 时落回系统 python, 由 weixin 入口的
# ImportError 兜底返回 failed 状态, 不影响其他 channel / 命令的可用性。
_HERMES_VENV_PYTHON = Path("/usr/local/lib/hermes-agent/venv/bin/python")
if _HERMES_VENV_PYTHON.exists() and Path(sys.executable).resolve() != _HERMES_VENV_PYTHON.resolve():
    os.execv(str(_HERMES_VENV_PYTHON), [str(_HERMES_VENV_PYTHON), __file__] + sys.argv[1:])


def main() -> int:
    # 命令行参数解析：当前只识别 --channel，后续可扩展更多渠道分支。
    p = argparse.ArgumentParser()
    p.add_argument("--channel", required=True)
    args = p.parse_args()
    return asyncio.run(_run(args.channel))


async def _run(channel: str) -> int:
    """消费 ocops.channel.channel_login 事件流，落回原 CLI 对外协议。

    核心登录逻辑已下沉到 ocops.channel.channel_login（async generator），
    本 shim 只负责把事件流翻译成原命令的输出契约（保持逐字不变）：
    - qrcode 事件 → 把 url 行写 stderr（沿用原 redirect_stdout(sys.stderr) 时代
      的「二维码 URL 打到 stderr」协议，manager 端按行抓取）
    - bound 终态  → stdout 单行 {"status":"bound"}，退出 0
    - timeout 终态→ stdout 单行 {"status":"timeout"}，退出 1
    - failed 终态 → stdout 单行 {"status":"failed","reason":...}，退出 1
    """
    from ocops.channel import channel_login

    async for event in channel_login(channel):
        kind = event.get("event")
        if kind == "qrcode":
            # 二维码 URL 写 stderr（manager 端流式抓取，转发给前端展示）。
            sys.stderr.write(event["url"] + "\n")
            sys.stderr.flush()
        elif kind == "bound":
            # 成功终态：凭证已由 qr_login 落盘到 /opt/data/weixin/accounts/。
            sys.stdout.write(json.dumps({"status": "bound"}) + "\n")
            return 0
        elif kind == "timeout":
            # 失败/超时终态：统一标记 timeout，manager 端按非 bound 处理。
            sys.stdout.write(json.dumps({"status": "timeout"}) + "\n")
            return 1
        elif kind == "failed":
            # 失败终态：附带原因（未知 channel / SDK 不可用 / 登录异常）。
            sys.stdout.write(json.dumps({"status": "failed", "reason": event.get("reason", "")}) + "\n")
            return 1

    # 事件流意外结束（理论上不会发生：generator 必以终态收尾）；
    # 兜底为 failed，避免 manager 端因无 stdout 输出而阻塞。
    sys.stdout.write(json.dumps({"status": "failed", "reason": "no terminal event"}) + "\n")
    return 1


if __name__ == "__main__":
    sys.exit(main())
