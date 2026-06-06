#!/usr/bin/env bash
# unit_test.sh — oc-lib.sh 纯函数单测，在 ops 镜像容器内跑（容器自带 bash/jq/coreutils）。
# 覆盖：s3_field 解析、creds 过期判断 needs_refresh 两个方向、write_aws_credentials 写出
# session token（STS 临时凭证）与省略 session token（长期凭证直发）两种场景。
set -uo pipefail
# shellcheck source=/usr/local/bin/oc-lib.sh
# shellcheck disable=SC1091
# 单测既可在 ops 镜像内跑（/usr/local/bin/oc-lib.sh），也可在仓库根目录直接跑。
# 直接运行时回退到相邻的 runtime/ops/bin/oc-lib.sh，便于本地验证 shell helper。
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
OC_LIB_UNDER_TEST="${OC_LIB_UNDER_TEST:-/usr/local/bin/oc-lib.sh}"
if [ ! -f "$OC_LIB_UNDER_TEST" ]; then
  OC_LIB_UNDER_TEST="$SCRIPT_DIR/../bin/oc-lib.sh"
fi
source "$OC_LIB_UNDER_TEST"
# oc-lib.sh 顶部的 set -e 会被 source 带入本 shell；显式关掉以收集多个断言失败而非首个即退出。
set +e

fail=0
assert_eq() { # <actual> <expected> <msg>
  if [ "$1" != "$2" ]; then echo "FAIL: $3：期望 '$2' 实得 '$1'"; fail=1; fi
}

# 构造一份 canned bootstrap JSON（远期过期，含 s3_write）。
cat > /tmp/bs.json <<'EOF'
{"manifest_yaml":"m","persona":"p","platform_rule":"r",
 "skills":[{"name":"weather","rel_path":"resources/skills/weather.tar","url":"http://x/w"}],
 "s3_write":{"endpoint":"http://minio:9000","region":"us-east-1","bucket":"oc-apps",
   "prefix":"apps/a1/","access_key_id":"AK","secret_access_key":"SK","session_token":"ST",
   "expires_at":"2099-01-01T00:00:00Z"}}
EOF

# s3_field 解析 bucket / prefix
assert_eq "$(s3_field /tmp/bs.json bucket)" "oc-apps" "s3_field bucket"
assert_eq "$(s3_field /tmp/bs.json prefix)" "apps/a1/" "s3_field prefix"

# needs_refresh：远期过期 → 不需刷新（needs_refresh 返回非 0）
exp=$(creds_expiry_epoch /tmp/bs.json)
if needs_refresh "$exp" 300; then echo "FAIL: 远期凭证不应判定需刷新"; fail=1; fi
# 已过期（epoch 0）→ 需刷新（返回 0）
if ! needs_refresh 0 300; then echo "FAIL: 已过期凭证应判定需刷新"; fail=1; fi

# write_aws_credentials 写出 ocsync profile，含 session token（STS 临时凭证场景）
HOME=/tmp/ochome write_aws_credentials /tmp/bs.json
grep -q '^aws_session_token = ST$' /tmp/ochome/.aws/credentials || { echo "FAIL: 凭证文件缺 session token"; fail=1; }
grep -q '^\[ocsync\]$' /tmp/ochome/.aws/credentials || { echo "FAIL: 凭证文件缺 ocsync profile 头"; fail=1; }

# 长期凭证场景（目标存储不支持 STS，manager 直发长期 key）：session_token 为空时不得写
# aws_session_token 行（空值会被 AWS CLI 当作非法 token 拒绝请求），但 ak/sk 仍须正常写入。
cat > /tmp/bs-longterm.json <<'EOF'
{"s3_write":{"endpoint":"http://minio:9000","region":"us-east-1","bucket":"oc-apps",
  "prefix":"apps/a1/","access_key_id":"LAK","secret_access_key":"LSK","session_token":"",
  "expires_at":"2099-01-01T00:00:00Z"}}
EOF
HOME=/tmp/ochome2 write_aws_credentials /tmp/bs-longterm.json
grep -q '^aws_access_key_id = LAK$' /tmp/ochome2/.aws/credentials || { echo "FAIL: 长期凭证缺 access key"; fail=1; }
if grep -q 'aws_session_token' /tmp/ochome2/.aws/credentials; then echo "FAIL: 长期凭证不应写 aws_session_token 行"; fail=1; fi

# ── sync_user_skills_up 跳过判定：受管(managed) / 内置(builtin) / 自创(user) ──
#
# 构造临时 data_dir，包含四种目录：
#   1. skills/managed-x/：含 SKILL.md + .oc-managed 标记 → 应跳过（受管 skill）
#   2. skills/builtin-y/：含 SKILL.md，其 frontmatter 规范名写入 .bundled_manifest → 应跳过
#      （内置 skill，且目录叶子名 builtin-y ≠ 规范名 builtin-y-canonical，验证按规范名匹配）
#   3. skills/created-z/：含 SKILL.md，无标记且不在基线 → 应被 aws_s3 sync 处理（自创 skill）
#   4. skills/category-c/：自身无 SKILL.md、真实 skill 在子目录 → 应跳过（镜像内置 category 容器目录）
TDATA=$(mktemp -d)
TBUNDLED=$(mktemp)
mkdir -p "$TDATA/skills/managed-x" "$TDATA/skills/builtin-y" "$TDATA/skills/created-z" "$TDATA/skills/category-c/leaf"
printf -- '---\nname: managed-x\n---\n' > "$TDATA/skills/managed-x/SKILL.md"
touch "$TDATA/skills/managed-x/.oc-managed"
printf -- '---\nname: builtin-y-canonical\n---\n' > "$TDATA/skills/builtin-y/SKILL.md"
printf -- '---\nname: created-z\n---\n' > "$TDATA/skills/created-z/SKILL.md"
# category-c 自身无 SKILL.md，模拟镜像内置的 category 容器（真实 skill 在 leaf 子目录）
printf -- '---\nname: leaf-skill\n---\n' > "$TDATA/skills/category-c/leaf/SKILL.md"
# 内置基线（.bundled_manifest 格式 "name:hash" 每行），含 builtin-y 的规范名
printf 'builtin-y-canonical:deadbeef\n' > "$TBUNDLED"

# mock aws_s3：把每次调用的第2个参数（本地路径）追加记录到 /tmp/aws_s3_calls.txt，
# 不实际调用 AWS CLI，避免依赖网络与凭证。
aws_s3() {
  # $1=sync $2=<local-dir> $3=<s3-url>
  printf '%s\n' "$2" >> /tmp/aws_s3_calls.txt
}
rm -f /tmp/aws_s3_calls.txt

# 设定 S3 环境变量（sync_user_skills_up 拼接 s3://... URL 时用到）
export AWS_S3_BUCKET="test-bucket"
export AWS_S3_PREFIX="apps/test/"
# 指定内置基线路径（.bundled_manifest 格式）
export OC_BUNDLED_MANIFEST="$TBUNDLED"

sync_user_skills_up "$TDATA"

# 断言：aws_s3 恰好只被调用了 1 次，且是对 created-z
# （managed-x 有 .oc-managed 跳过；builtin-y 规范名在基线跳过；category-c 无 SKILL.md 跳过）
CALLS=$(cat /tmp/aws_s3_calls.txt 2>/dev/null || true)
CALL_COUNT=$(printf '%s\n' "$CALLS" | grep -c . 2>/dev/null || true)
# 只有 created-z 被同步（调用次数恰好为 1）
assert_eq "$CALL_COUNT" "1" "sync_user_skills_up 应只对 created-z 调 aws_s3（共 1 次）"
# 被同步的路径以 created-z/ 结尾
ACTUAL_PATH=$(printf '%s\n' "$CALLS" | head -1)
EXPECTED_PATH="$TDATA/skills/created-z/"
assert_eq "$ACTUAL_PATH" "$EXPECTED_PATH" "sync_user_skills_up 调 aws_s3 的本地路径应为 created-z/"

# 清理临时目录
rm -rf "$TDATA" "$TBUNDLED"
rm -f /tmp/aws_s3_calls.txt

# ── restore_longterm_memory_down 根级记忆文件恢复：区分缺失对象与真实 S3 故障 ──
#
# mock aws_s3：sync 总是成功；ls 根据 RESTORE_MEMORY_MODE 模拟三类结果；
# cp 只记录调用，不实际下载，避免依赖真实 S3。
aws_s3() {
  printf '%s\n' "$*" >> "$RESTORE_MEMORY_CALLS"
  case "$1" in
    sync)
      return 0
      ;;
    ls)
      case "$RESTORE_MEMORY_MODE:$2" in
        missing:*)
          printf 'fatal error: An error occurred (404) when calling the HeadObject operation: Not Found\n' >&2
          return 1
          ;;
        failure:*)
          printf 'fatal error: An error occurred (AccessDenied) when calling the HeadObject operation: denied\n' >&2
          return 1
          ;;
        memory_exists:*MEMORY.md)
          return 0
          ;;
        memory_exists:*)
          printf 'fatal error: An error occurred (NoSuchKey) when calling the HeadObject operation: Key does not exist\n' >&2
          return 1
          ;;
      esac
      ;;
    cp)
      return 0
      ;;
  esac
  printf 'unexpected aws_s3 call: %s\n' "$*" >&2
  return 2
}

export AWS_S3_BUCKET="test-bucket"
export AWS_S3_PREFIX="apps/test/"

# 根级记忆文件缺失属于首启/未生成记忆场景，应跳过并整体成功。
RESTORE_MEMORY_CALLS=$(mktemp)
RESTORE_MEMORY_MODE="missing"
TDATA=$(mktemp -d)
restore_longterm_memory_down "$TDATA"
RC=$?
assert_eq "$RC" "0" "restore_longterm_memory_down 应跳过缺失的根级记忆文件"
if grep -q '^cp ' "$RESTORE_MEMORY_CALLS"; then echo "FAIL: 缺失根级记忆文件时不应调用 cp"; fail=1; fi
rm -rf "$TDATA"
rm -f "$RESTORE_MEMORY_CALLS"

# 非缺失类 ls 错误代表认证、网络或服务端故障，应返回非零让 oc-restore 失败。
RESTORE_MEMORY_CALLS=$(mktemp)
RESTORE_MEMORY_LOG=$(mktemp)
RESTORE_MEMORY_MODE="failure"
TDATA=$(mktemp -d)
restore_longterm_memory_down "$TDATA" 2>"$RESTORE_MEMORY_LOG"
RC=$?
if [ "$RC" -eq 0 ]; then echo "FAIL: restore_longterm_memory_down 遇到真实 S3 ls 故障应返回非零"; fail=1; fi
if grep -q '^cp ' "$RESTORE_MEMORY_CALLS"; then echo "FAIL: 真实 S3 ls 故障时不应继续调用 cp"; fail=1; fi
rm -rf "$TDATA"
rm -f "$RESTORE_MEMORY_CALLS" "$RESTORE_MEMORY_LOG"

# MEMORY.md 存在时应执行 cp；USER.md 缺失时仍应按可选根级文件跳过。
RESTORE_MEMORY_CALLS=$(mktemp)
RESTORE_MEMORY_MODE="memory_exists"
TDATA=$(mktemp -d)
restore_longterm_memory_down "$TDATA"
RC=$?
assert_eq "$RC" "0" "restore_longterm_memory_down 应恢复存在的根级记忆文件"
if ! grep -q '^cp s3://test-bucket/apps/test/MEMORY.md ' "$RESTORE_MEMORY_CALLS"; then echo "FAIL: MEMORY.md 存在时应调用 cp"; fail=1; fi
if grep -q '^cp s3://test-bucket/apps/test/USER.md ' "$RESTORE_MEMORY_CALLS"; then echo "FAIL: USER.md 缺失时不应调用 cp"; fail=1; fi
rm -rf "$TDATA"
rm -f "$RESTORE_MEMORY_CALLS"

if [ "$fail" -eq 0 ]; then echo "unit_test: ALL PASS"; fi
exit "$fail"
