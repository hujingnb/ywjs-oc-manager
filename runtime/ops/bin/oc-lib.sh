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

# write_aws_credentials <json> 从 s3_write 把凭证写入 ~/.aws/credentials 的 ocsync profile，
# 供 aws_s3 用 --profile ocsync 调用。权限 0600。
write_aws_credentials() {
  local json="$1" ak sk st
  ak=$(jq -r '.s3_write.access_key_id' "$json")
  sk=$(jq -r '.s3_write.secret_access_key' "$json")
  st=$(jq -r '.s3_write.session_token' "$json")
  [ -n "$ak" ] && [ "$ak" != "null" ] || { log "s3_write.access_key_id 缺失"; return 1; }
  # mkdir 与文件写入同在 umask 077 子 shell 内，保证 ~/.aws 目录 0700、credentials 文件 0600。
  # session_token 仅 STS 临时凭证才有；目标存储不支持 STS 时 manager 直发长期凭证，
  # s3_write.session_token 为空/null，此时绝不能写 aws_session_token 行——空值会被 AWS CLI
  # 当作非法 token 而拒绝所有请求。
  ( umask 077
    mkdir -p "$HOME/.aws"
    {
      printf '[ocsync]\n'
      printf 'aws_access_key_id = %s\n' "$ak"
      printf 'aws_secret_access_key = %s\n' "$sk"
      if [ -n "$st" ] && [ "$st" != "null" ]; then
        printf 'aws_session_token = %s\n' "$st"
      fi
    } > "$HOME/.aws/credentials"
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

# sync_sessions_up 把本地 sessions 目录增量同步到 S3（无 --delete）。
# 会话附属文件（request_dump / sessions.json 等），与 workspace 并列的 app 级持久数据
# （父设计 §5.4）；不排除任何目录（sessions 无可重建大目录）。
sync_sessions_up() {
  local data_dir="$1"
  aws_s3 sync "$data_dir/sessions" "s3://${AWS_S3_BUCKET}/${AWS_S3_PREFIX}sessions/"
}

# sync_weixin_up 把本地 weixin 渠道凭证目录增量同步到 S3（无 --delete）。
# qr_login 扫码绑定后凭证落盘到 /opt/data/weixin/accounts/<id>.json（含同步缓冲
# <id>.sync.json）；这是 app 级持久数据，必须随 workspace/sessions 一起持久化，
# 否则 pod 重启（尤其绑定后触发的渠道重载 RolloutRestart）会丢失登录态，网关重启后
# 报 "No messaging platforms enabled"、已绑定渠道失活。目录不存在时 aws s3 sync 空操作。
sync_weixin_up() {
  local data_dir="$1"
  [ -d "$data_dir/weixin" ] || return 0
  aws_s3 sync "$data_dir/weixin" "s3://${AWS_S3_BUCKET}/${AWS_S3_PREFIX}weixin/"
}

# sync_user_skills_up <data_dir> 把 skills/ 下「无 .oc-managed 且不在内置清单」的自创 skill
# 增量同步到 S3 apps/<id>/skills/<name>/。
# 跳过条件（两类）：
#   1. 目录含 .oc-managed 文件 → 受平台管理的内置/托管 skill，不由用户自持；
#   2. 目录名出现在 ${OC_BUILTIN_MANIFEST:-/opt/skills-builtin.json} 的 .builtin 数组内
#      → 内置 skill，也不上传（文件不存在则视为无内置清单，仅跳过有 .oc-managed 的目录）。
# skills/ 不存在时静默跳过（首启尚未创建自定义 skill）。
sync_user_skills_up() {
  local data_dir="$1"
  local skills_dir="$data_dir/skills"
  [ -d "$skills_dir" ] || return 0
  local builtin_file="${OC_BUILTIN_MANIFEST:-/opt/skills-builtin.json}"
  local dir name
  for dir in "$skills_dir"/*/; do
    [ -d "$dir" ] || continue
    name=$(basename "$dir")
    # 跳过受管 skill：目录内有 .oc-managed 标记文件
    [ -f "$dir/.oc-managed" ] && continue
    # 跳过内置 skill：名字出现在内置清单数组 .builtin 中
    if [ -f "$builtin_file" ] && jq -e --arg n "$name" '.builtin | index($n)' "$builtin_file" >/dev/null 2>&1; then
      continue
    fi
    # 自创 skill：同步到 S3（不加 --delete，以免删掉 S3 侧未落盘的内容）
    aws_s3 sync "$dir" "s3://${AWS_S3_BUCKET}/${AWS_S3_PREFIX}skills/${name}/"
  done
}

# restore_user_skills <data_dir> 从 S3 apps/<id>/skills/ 把所有自创 skill 还原到 skills/。
# 首启时前缀为空，aws s3 sync 返回 0 不报错（幂等）。
restore_user_skills() {
  local data_dir="$1"
  mkdir -p "$data_dir/skills"
  aws_s3 sync "s3://${AWS_S3_BUCKET}/${AWS_S3_PREFIX}skills/" "$data_dir/skills/"
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
