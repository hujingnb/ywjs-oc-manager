#!/usr/bin/env bash
# shellcheck shell=bash
# oc-lib.sh — ops 脚本共享函数库。由 oc-restore / oc-sync / oc-presync 通过 source 引入。
# 职责：日志、bootstrap 拉取与重试、STS 凭证解析/写入与过期判断、aws s3 调用包装、
# workspace 同步与 sqlite 一致性备份。所有函数无副作用地依赖调用方已 export 的 env。
set -euo pipefail

# log 输出带 UTC 时间戳的日志到 stderr（不污染脚本 stdout）。
log() { printf '[%s] %s\n' "$(date -u +%FT%TZ)" "$*" >&2; }

# require_env 校验列出的环境变量均非空，缺失则报错返回 1。
require_env() {
  local v
  for v in "$@"; do
    [ -n "${!v:-}" ] || { log "缺少必需环境变量: $v"; return 1; }
  done
}

# fetch_bootstrap <out_file> 带 Bearer control token 调 bootstrap，指数退避重试。
# OC_BOOTSTRAP_RETRIES（默认 5）是总请求次数（含首次）；成功把响应 JSON 写入 out_file 并返回 0，
# 用尽全部尝试仍失败返回 1（调用方据此决定是否非零退出）。
fetch_bootstrap() {
  local out="$1" attempt=0 max="${OC_BOOTSTRAP_RETRIES:-5}" delay=1
  while :; do
    if curl -fsS -H "Authorization: Bearer ${OC_CONTROL_TOKEN}" "${OC_BOOTSTRAP_URL}" -o "$out"; then
      return 0
    fi
    attempt=$((attempt + 1))
    if [ "$attempt" -ge "$max" ]; then
      log "bootstrap 拉取失败，已重试 ${max} 次"
      return 1
    fi
    log "bootstrap 拉取失败，第 ${attempt}/${max} 次，${delay}s 后重试"
    sleep "$delay"
    delay=$((delay * 2))
  done
}

# s3_field <json> <field> 输出 s3_write.<field>（endpoint/region/bucket/prefix 等）。
s3_field() { jq -r ".s3_write.$2" "$1"; }

# export_s3_env <json> 从 s3_write 导出 aws_s3 包装所需的 S3 参数到当前 shell 环境。
export_s3_env() {
  AWS_S3_ENDPOINT=$(s3_field "$1" endpoint)
  AWS_S3_REGION=$(s3_field "$1" region)
  AWS_S3_BUCKET=$(s3_field "$1" bucket)
  AWS_S3_PREFIX=$(s3_field "$1" prefix)
  export AWS_S3_ENDPOINT AWS_S3_REGION AWS_S3_BUCKET AWS_S3_PREFIX
}

# write_aws_credentials <json> 从 s3_write 把 STS 临时凭证写入 ~/.aws/credentials 的 ocsync profile
# （含 session token），供 aws_s3 用 --profile ocsync 调用。权限 0600。
write_aws_credentials() {
  local json="$1" ak sk st
  ak=$(jq -r '.s3_write.access_key_id' "$json")
  sk=$(jq -r '.s3_write.secret_access_key' "$json")
  st=$(jq -r '.s3_write.session_token' "$json")
  [ -n "$ak" ] && [ "$ak" != "null" ] || { log "s3_write.access_key_id 缺失"; return 1; }
  # mkdir 与文件写入同在 umask 077 子 shell 内，保证 ~/.aws 目录 0700、credentials 文件 0600。
  ( umask 077; mkdir -p "$HOME/.aws"; cat > "$HOME/.aws/credentials" <<EOF
[ocsync]
aws_access_key_id = ${ak}
aws_secret_access_key = ${sk}
aws_session_token = ${st}
EOF
  )
}

# creds_expiry_epoch <json> 输出 s3_write.expires_at（RFC3339）对应的 epoch 秒（GNU date，由 coreutils 提供）。
creds_expiry_epoch() {
  local exp
  exp=$(jq -r '.s3_write.expires_at' "$1")
  date -u -d "$exp" +%s
}

# needs_refresh <expiry_epoch> <skew_seconds> 当凭证剩余有效期 < skew 时返回 0（需刷新），否则返回 1。
needs_refresh() {
  local now
  now=$(date -u +%s)
  [ $(( $1 - now )) -lt "$2" ]
}

# aws_s3 <args...> 用 ocsync profile + bootstrap 给的 endpoint/region 调 aws s3 子命令。
aws_s3() {
  aws --profile ocsync --endpoint-url "$AWS_S3_ENDPOINT" --region "$AWS_S3_REGION" s3 "$@"
}

# sync_workspace_up 把本地 workspace 增量同步到 S3（排除可重建大目录；不加 --delete 以免误删持久数据）。
sync_workspace_up() {
  local data_dir="$1"
  aws_s3 sync "$data_dir/workspace" "s3://${AWS_S3_BUCKET}/${AWS_S3_PREFIX}workspace/" \
    --exclude "node_modules/*" --exclude ".git/*" --exclude "*.tmp"
}

# backup_sqlite_up 用 sqlite .backup 出 live DB 的一致性快照并上传为 state.db（绝不分别传 -wal/-shm）。
# 本地无 state.db（首启未建库）时静默跳过。
backup_sqlite_up() {
  local data_dir="$1"
  [ -f "$data_dir/state.db" ] || return 0
  # 用唯一临时文件：preStop（oc-presync）触发时 sidecar 的 oc-sync 主循环仍在运行，
  # 两者可能并发调用本函数；固定路径会相互覆盖/删除导致上传半写的损坏快照。
  local snap
  snap=$(mktemp /tmp/oc-snap.XXXXXX.db)
  sqlite3 "$data_dir/state.db" ".backup $snap"
  aws_s3 cp "$snap" "s3://${AWS_S3_BUCKET}/${AWS_S3_PREFIX}state.db"
  rm -f "$snap"
}
