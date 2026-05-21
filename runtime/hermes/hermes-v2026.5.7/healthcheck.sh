#!/usr/bin/env bash
# Hermes gateway 健康检查；内部 hermes gateway status 退出码即为健康判定。
set -euo pipefail
exec hermes gateway status >/dev/null 2>&1
