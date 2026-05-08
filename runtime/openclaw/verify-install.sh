#!/usr/bin/env sh
# build-time 校验：openclaw 二进制可执行 + 微信插件已安装。
# 不执行 `plugins list`（会触发完整 plugin 加载耗时较久），只查文件系统。
#
# OpenClaw `plugins install` 在不同上游版本 plugin 装到不同路径（2026.4.29 装
# /root/.openclaw/extensions/，2026.5.7 改装到全局 npm 路径），用 find 兼容。
set -eu

openclaw --version
MANIFEST=$(find / -name openclaw.plugin.json -path '*openclaw-weixin*' 2>/dev/null | head -1)
[ -n "$MANIFEST" ] || { echo "openclaw-weixin plugin missing (no manifest found)" >&2; exit 1; }
test -f "$MANIFEST" || { echo "openclaw-weixin manifest path invalid: $MANIFEST" >&2; exit 1; }
echo "verify-install: ok (manifest at $MANIFEST)"
