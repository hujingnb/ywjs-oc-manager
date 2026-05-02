#!/usr/bin/env sh
# OpenClaw 健康检查：直接打 gateway 的 HTTP /healthz。
# 上游会返回 {"ok":true,"status":"live"}，HTTP 200 视为健康。
# Sprint 0 POC 验证：CLI 检查太慢（每次 exec 重新加载 118 个 plugin 约 11s），用 HTTP 探针。
set -eu

curl -fsS --max-time 5 http://127.0.0.1:18789/healthz >/dev/null
