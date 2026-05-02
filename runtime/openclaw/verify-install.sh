#!/usr/bin/env sh
# build-time 校验：openclaw 二进制可执行 + 微信插件已安装。
# 不执行 `plugins list`（会触发完整 plugin 加载耗时较久），只查文件系统。
set -eu

openclaw --version
test -d /root/.openclaw/extensions/openclaw-weixin || { echo "openclaw-weixin plugin missing" >&2; exit 1; }
test -f /root/.openclaw/extensions/openclaw-weixin/openclaw.plugin.json
echo "verify-install: ok"
