#!/usr/bin/env bash
# 进入 manager-redis 容器的 redis-cli 交互终端。
#
# 用法:
#   ./redis-cli.sh                       # 进入交互式 redis-cli
#   ./redis-cli.sh PING                  # 单条命令执行后退出
#   ./redis-cli.sh KEYS 'ocm:jobs:*'     # 任意 redis-cli 子命令
#
# 密码来自同目录 .env,通过 REDISCLI_AUTH 环境变量传入,避免在命令行出现。
set -euo pipefail

# 切到脚本所在目录,确保能读取同目录下的 docker-compose.yml 与 .env。
cd "$(dirname "$0")"

if [[ ! -f ./.env ]]; then
  echo "缺少 .env 文件,请基于 .env.example 复制并填充真实值" >&2
  exit 1
fi

set -a
# shellcheck disable=SC1091
source ./.env
set +a

: "${MANAGER_REDIS_PASSWORD:?未在 .env 中配置}"

# REDISCLI_AUTH 是 redis-cli 官方支持的环境变量,等价于 -a 但不会触发命令行明文告警。
exec docker compose exec \
  -e REDISCLI_AUTH="${MANAGER_REDIS_PASSWORD}" \
  manager-redis \
  redis-cli "$@"
