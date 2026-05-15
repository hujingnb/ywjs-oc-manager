#!/usr/bin/env bash
# 进入 manager-postgres 容器的 psql 交互终端。
#
# 用法:
#   ./psql.sh                        # 进入交互式 psql
#   ./psql.sh -c "SELECT now()"      # 单条命令执行后退出
#   ./psql.sh -f /path/to/file.sql   # 执行 SQL 文件(需在容器内可见)
#
# 任何额外参数都透传给容器内 psql。
# 数据库用户名 / 库名 / 密码均来自同目录 .env,避免脚本里硬编码任何凭据。
set -euo pipefail

# 切到脚本所在目录,确保能读取同目录下的 docker-compose.yml 与 .env。
cd "$(dirname "$0")"

if [[ ! -f ./.env ]]; then
  echo "缺少 .env 文件,请基于 .env.example 复制并填充真实值" >&2
  exit 1
fi

# set -a 让 source 出来的变量自动 export,后续 docker compose 子进程也能继承。
set -a
# shellcheck disable=SC1091
source ./.env
set +a

: "${MANAGER_POSTGRES_USER:?未在 .env 中配置}"
: "${MANAGER_POSTGRES_DB:?未在 .env 中配置}"
: "${MANAGER_POSTGRES_PASSWORD:?未在 .env 中配置}"

# 通过 PGPASSWORD 环境变量把密码传给容器内 psql,避免出现在 ps / 历史命令里;
# -e 让 docker compose exec 把变量注入到容器内 psql 进程的环境。
exec docker compose exec \
  -e PGPASSWORD="${MANAGER_POSTGRES_PASSWORD}" \
  manager-postgres \
  psql -U "${MANAGER_POSTGRES_USER}" -d "${MANAGER_POSTGRES_DB}" "$@"
