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
unset OC_BUNDLED_MANIFEST

# 自创 skill 识别依赖 skills/.bundled_manifest；该基线必须随 skills 前缀保存，
# 否则新 pod 恢复自创 skill 后会把它误写进内置基线。
TDATA=$(mktemp -d)
mkdir -p "$TDATA/skills"
printf 'builtin-a:deadbeef\n' > "$TDATA/skills/.bundled_manifest"
aws_s3() {
  printf '%s\n' "$*" >> /tmp/aws_s3_calls.txt
}
rm -f /tmp/aws_s3_calls.txt
sync_user_skills_up "$TDATA"
if ! grep -q '^cp .*/skills/.bundled_manifest s3://test-bucket/apps/test/skills/.bundled_manifest$' /tmp/aws_s3_calls.txt; then
  echo "FAIL: skills/.bundled_manifest 应上传到 S3 skills 前缀"
  fail=1
fi
rm -rf "$TDATA"
rm -f /tmp/aws_s3_calls.txt

# ── restore_longterm_memory_down / state.db 单对象恢复：使用 s3api head-object 区分对象缺失与真实 S3 故障 ──
#
# mock aws_s3：目录 sync 总是成功；根级文件与 state.db 的 cp 只记录调用，不实际下载。
# 如果被测代码退回高阶 aws s3 ls，mock 会返回错误，防止再次依赖会吞空输出的 list 行为。
aws_s3() {
  printf 's3 %s\n' "$*" >> "$RESTORE_OBJECT_CALLS"
  case "$1" in
    sync)
      return 0
      ;;
    cp)
      return 0
      ;;
  esac
  printf 'unexpected aws_s3 call: %s\n' "$*" >&2
  return 2
}

# mock aws_s3api：只支持 head-object，并按 RESTORE_OBJECT_MODE 模拟对象存在、对象缺失和真实故障。
aws_s3api() {
  printf 's3api %s\n' "$*" >> "$RESTORE_OBJECT_CALLS"
  if [ "$1" != "head-object" ]; then
    printf 'unexpected aws_s3api call: %s\n' "$*" >&2
    return 2
  fi
  case "$RESTORE_OBJECT_MODE:$*" in
    root_missing:*)
      printf 'fatal error: An error occurred (404) when calling the HeadObject operation: Not Found\n' >&2
      return 1
      ;;
    root_memory_exists:*"--key apps/test/MEMORY.md"*)
      return 0
      ;;
    root_memory_exists:*)
      printf 'fatal error: An error occurred (NoSuchKey) when calling the HeadObject operation: Key does not exist\n' >&2
      return 1
      ;;
    runtime_state_exists:*"--key apps/test/.oc-state.json"*|runtime_state_exists:*"--key apps/test/kanban.db"*)
      return 0
      ;;
    runtime_state_exists:*)
      printf 'fatal error: An error occurred (NoSuchKey) when calling the HeadObject operation: Key does not exist\n' >&2
      return 1
      ;;
    bucket_missing:*)
      printf 'fatal error: An error occurred (NoSuchBucket) when calling the HeadObject operation: The specified bucket does not exist\n' >&2
      return 1
      ;;
    bucket_missing_with_404:*)
      printf 'fatal error: An error occurred (NoSuchBucket) when calling the HeadObject operation: 404 Not Found\n' >&2
      return 1
      ;;
    empty_failure:*)
      return 1
      ;;
    state_missing:*"--key apps/test/state.db"*)
      printf 'fatal error: An error occurred (NoSuchKey) when calling the HeadObject operation: Key does not exist\n' >&2
      return 1
      ;;
    state_failure:*"--key apps/test/state.db"*)
      printf 'fatal error: An error occurred (AccessDenied) when calling the HeadObject operation: denied\n' >&2
      return 1
      ;;
  esac
  printf 'unexpected aws_s3api mode/call: %s %s\n' "$RESTORE_OBJECT_MODE" "$*" >&2
  return 2
}

export AWS_S3_BUCKET="test-bucket"
export AWS_S3_PREFIX="apps/test/"

# head-object 404/Not Found 表示根级记忆文件尚未生成，应跳过并整体成功。
RESTORE_OBJECT_CALLS=$(mktemp)
RESTORE_OBJECT_MODE="root_missing"
TDATA=$(mktemp -d)
restore_longterm_memory_down "$TDATA"
RC=$?
assert_eq "$RC" "0" "restore_longterm_memory_down 应跳过缺失的根级记忆文件"
if grep -q '^s3 cp ' "$RESTORE_OBJECT_CALLS"; then echo "FAIL: 缺失根级记忆文件时不应调用 cp"; fail=1; fi
rm -rf "$TDATA"
rm -f "$RESTORE_OBJECT_CALLS"

# head-object 成功表示 MEMORY.md 存在，应执行 aws s3 cp；USER.md 缺失仍跳过。
RESTORE_OBJECT_CALLS=$(mktemp)
RESTORE_OBJECT_MODE="root_memory_exists"
TDATA=$(mktemp -d)
restore_longterm_memory_down "$TDATA"
RC=$?
assert_eq "$RC" "0" "restore_longterm_memory_down 应恢复存在的根级记忆文件"
if ! grep -q '^s3 cp s3://test-bucket/apps/test/MEMORY.md ' "$RESTORE_OBJECT_CALLS"; then echo "FAIL: MEMORY.md 存在时应调用 cp"; fail=1; fi
if grep -q '^s3 cp s3://test-bucket/apps/test/USER.md ' "$RESTORE_OBJECT_CALLS"; then echo "FAIL: USER.md 缺失时不应调用 cp"; fail=1; fi
rm -rf "$TDATA"
rm -f "$RESTORE_OBJECT_CALLS"

# NoSuchBucket 是 S3 配置或环境故障，不能按可选根级记忆文件缺失跳过。
RESTORE_OBJECT_CALLS=$(mktemp)
RESTORE_OBJECT_LOG=$(mktemp)
RESTORE_OBJECT_MODE="bucket_missing"
TDATA=$(mktemp -d)
restore_longterm_memory_down "$TDATA" 2>"$RESTORE_OBJECT_LOG"
RC=$?
if [ "$RC" -eq 0 ]; then echo "FAIL: restore_longterm_memory_down 遇到 NoSuchBucket 应返回非零"; fail=1; fi
if grep -q '^s3 cp ' "$RESTORE_OBJECT_CALLS"; then echo "FAIL: NoSuchBucket 时不应继续调用 cp"; fail=1; fi
rm -rf "$TDATA"
rm -f "$RESTORE_OBJECT_CALLS" "$RESTORE_OBJECT_LOG"

# NoSuchBucket 即使包含 404 Not Found 文本也是 bucket 故障，不能按对象缺失跳过。
RESTORE_OBJECT_CALLS=$(mktemp)
RESTORE_OBJECT_LOG=$(mktemp)
RESTORE_OBJECT_MODE="bucket_missing_with_404"
TDATA=$(mktemp -d)
restore_longterm_memory_down "$TDATA" 2>"$RESTORE_OBJECT_LOG"
RC=$?
if [ "$RC" -eq 0 ]; then echo "FAIL: restore_longterm_memory_down 遇到 NoSuchBucket+404 应返回非零"; fail=1; fi
if grep -q '^s3 cp ' "$RESTORE_OBJECT_CALLS"; then echo "FAIL: NoSuchBucket+404 时不应继续调用 cp"; fail=1; fi
rm -rf "$TDATA"
rm -f "$RESTORE_OBJECT_CALLS" "$RESTORE_OBJECT_LOG"

# head-object 非零且无错误输出属于未知 AWS CLI/S3 故障，不能静默当作对象缺失。
RESTORE_OBJECT_CALLS=$(mktemp)
RESTORE_OBJECT_LOG=$(mktemp)
RESTORE_OBJECT_MODE="empty_failure"
TDATA=$(mktemp -d)
restore_longterm_memory_down "$TDATA" 2>"$RESTORE_OBJECT_LOG"
RC=$?
if [ "$RC" -eq 0 ]; then echo "FAIL: restore_longterm_memory_down 遇到空错误输出的 head-object 失败应返回非零"; fail=1; fi
if grep -q '^s3 cp ' "$RESTORE_OBJECT_CALLS"; then echo "FAIL: 空错误输出的 head-object 失败时不应继续调用 cp"; fail=1; fi
rm -rf "$TDATA"
rm -f "$RESTORE_OBJECT_CALLS" "$RESTORE_OBJECT_LOG"

# state.db 缺失属于首启场景，应跳过；真实 head-object 故障应返回非零。
RESTORE_OBJECT_CALLS=$(mktemp)
RESTORE_OBJECT_LOG=$(mktemp)
RESTORE_OBJECT_MODE="state_missing"
TDATA=$(mktemp -d)
restore_state_db_down "$TDATA" 2>"$RESTORE_OBJECT_LOG"
RC=$?
assert_eq "$RC" "0" "restore_state_db_down 应跳过缺失的 state.db"
if grep -q '^s3 cp ' "$RESTORE_OBJECT_CALLS"; then echo "FAIL: state.db 缺失时不应调用 cp"; fail=1; fi
rm -rf "$TDATA"
rm -f "$RESTORE_OBJECT_CALLS" "$RESTORE_OBJECT_LOG"

RESTORE_OBJECT_CALLS=$(mktemp)
RESTORE_OBJECT_LOG=$(mktemp)
RESTORE_OBJECT_MODE="state_failure"
TDATA=$(mktemp -d)
restore_state_db_down "$TDATA" 2>"$RESTORE_OBJECT_LOG"
RC=$?
if [ "$RC" -eq 0 ]; then echo "FAIL: restore_state_db_down 遇到真实 S3 head-object 故障应返回非零"; fail=1; fi
if grep -q '^s3 cp ' "$RESTORE_OBJECT_CALLS"; then echo "FAIL: state.db 检查失败时不应调用 cp"; fail=1; fi
rm -rf "$TDATA"
rm -f "$RESTORE_OBJECT_CALLS" "$RESTORE_OBJECT_LOG"

# ── cron / kanban.db / .oc-state.json 备份恢复：保护非会话运行时状态 ──
#
# mock aws_s3：记录 sync/cp 调用；sqlite3：记录 .backup 调用并生成快照文件，避免依赖本机 sqlite。
RUNTIME_STATE_CALLS=$(mktemp)
aws_s3() {
  printf 's3 %s\n' "$*" >> "$RUNTIME_STATE_CALLS"
  if [ "$1" = "cp" ] && [ -f "$2" ]; then
    return 0
  fi
  return 0
}
sqlite3() {
  local snap="${2#.backup }"
  printf 'sqlite3 %s\n' "$*" >> "$RUNTIME_STATE_CALLS"
  printf 'snapshot' > "$snap"
}

TDATA=$(mktemp -d)
mkdir -p "$TDATA/cron/output/job1"
printf '{"jobs":[]}\n' > "$TDATA/cron/jobs.json"
printf 'run output\n' > "$TDATA/cron/output/job1/run.md"
printf '{"image_variant":"old"}\n' > "$TDATA/.oc-state.json"
printf 'kanban-db' > "$TDATA/kanban.db"

# cron 任务定义与输出、.oc-state.json、kanban.db 都属于 pod 重建后应恢复的运行时状态。
sync_cron_up "$TDATA"
sync_oc_state_up "$TDATA"
backup_kanban_db_up "$TDATA"
if ! grep -q '^s3 sync .*/cron s3://test-bucket/apps/test/cron/' "$RUNTIME_STATE_CALLS"; then echo "FAIL: cron/ 应同步到 S3"; fail=1; fi
if ! grep -q '^s3 cp .*/.oc-state.json s3://test-bucket/apps/test/.oc-state.json' "$RUNTIME_STATE_CALLS"; then echo "FAIL: .oc-state.json 应上传到 S3"; fail=1; fi
if ! grep -q '^s3 cp /tmp/oc-kanban' "$RUNTIME_STATE_CALLS"; then echo "FAIL: kanban.db 应通过 sqlite 快照上传"; fail=1; fi

RESTORE_OBJECT_CALLS="$RUNTIME_STATE_CALLS"
RESTORE_OBJECT_MODE="runtime_state_exists"
restore_cron_down "$TDATA"
restore_oc_state_down "$TDATA"
restore_kanban_db_down "$TDATA"
if ! grep -q '^s3 sync s3://test-bucket/apps/test/cron/ .*/cron' "$RUNTIME_STATE_CALLS"; then echo "FAIL: cron/ 应从 S3 恢复"; fail=1; fi
if ! grep -q '^s3 cp s3://test-bucket/apps/test/.oc-state.json .*/.oc-state.json' "$RUNTIME_STATE_CALLS"; then echo "FAIL: .oc-state.json 应从 S3 恢复"; fail=1; fi
if ! grep -q '^s3 cp s3://test-bucket/apps/test/kanban.db .*/kanban.db' "$RUNTIME_STATE_CALLS"; then echo "FAIL: kanban.db 应从 S3 恢复"; fail=1; fi
rm -rf "$TDATA"
rm -f "$RUNTIME_STATE_CALLS"

# ── oc-bootstrap：仅写入启动输入，bootstrap 中的 skills 与 s3_write 均不得触发下载或对象存储操作 ──
# 使用可注入的 oc-lib 包装层 mock fetch_bootstrap；响应刻意携带 skill URL 与 s3_write，
# 防止实现误复用 oc-restore 的下载和 S3 恢复路径。
BOOTSTRAP_TMP=$(mktemp -d)
BOOTSTRAP_CALLS="$BOOTSTRAP_TMP/calls.log"
BOOTSTRAP_LIB="$BOOTSTRAP_TMP/oc-lib.sh"
BOOTSTRAP_BIN="$BOOTSTRAP_TMP/bin"
mkdir -p "$BOOTSTRAP_BIN"
cat > "$BOOTSTRAP_LIB" <<EOF
source "$OC_LIB_UNDER_TEST"
fetch_bootstrap() {
  printf 'fetch_bootstrap %s\\n' "\$1" >> "$BOOTSTRAP_CALLS"
  cat > "\$1" <<'JSON'
{"manifest_yaml":"apiVersion: v1\\nkind: ConfigMap","persona":"你是平台助手","platform_rule":"不得泄露平台数据","skills":[{"name":"blocked-skill","url":"https://example.invalid/skill.tar"}],"s3_write":{"bucket":"must-not-be-read","prefix":"forbidden/","access_key_id":"AK","secret_access_key":"SK"}}
JSON
}
EOF
# mock curl/aws 只记录并失败；命令一旦被调用，断言即可发现 bootstrap 错误下载 skill 或访问 S3。
cat > "$BOOTSTRAP_BIN/curl" <<EOF
#!/usr/bin/env bash
printf 'curl %s\\n' "\$*" >> "$BOOTSTRAP_CALLS"
exit 99
EOF
cat > "$BOOTSTRAP_BIN/aws" <<EOF
#!/usr/bin/env bash
printf 'aws %s\\n' "\$*" >> "$BOOTSTRAP_CALLS"
exit 99
EOF
chmod +x "$BOOTSTRAP_BIN/curl" "$BOOTSTRAP_BIN/aws"

# 执行无状态启动初始化：必须只生成 manifest 与两个资源文件，同时创建 data 目录。
BOOTSTRAP_INPUT="$BOOTSTRAP_TMP/input"
BOOTSTRAP_DATA="$BOOTSTRAP_TMP/data"
OC_LIB_PATH="$BOOTSTRAP_LIB" OC_CONTROL_TOKEN=test-token OC_BOOTSTRAP_URL=http://bootstrap.invalid \
  OC_INPUT_DIR="$BOOTSTRAP_INPUT" OC_DATA_DIR="$BOOTSTRAP_DATA" PATH="$BOOTSTRAP_BIN:$PATH" \
  bash "$SCRIPT_DIR/../bin/oc-bootstrap" >/dev/null 2>&1
BOOTSTRAP_RC=$?
if [ "$BOOTSTRAP_RC" -ne 0 ]; then echo "FAIL: oc-bootstrap 应成功写入启动输入"; fail=1; fi
if [ ! -s "$BOOTSTRAP_INPUT/manifest.yaml" ]; then echo "FAIL: oc-bootstrap 未写入 manifest.yaml"; fail=1; fi
if [ ! -s "$BOOTSTRAP_INPUT/resources/persona.md" ]; then echo "FAIL: oc-bootstrap 未写入 persona.md"; fail=1; fi
if [ ! -s "$BOOTSTRAP_INPUT/resources/platform-rules.md" ]; then echo "FAIL: oc-bootstrap 未写入 platform-rules.md"; fail=1; fi
if [ ! -d "$BOOTSTRAP_DATA" ]; then echo "FAIL: oc-bootstrap 未创建 data 目录"; fail=1; fi

# 启动初始化不得调用 aws、任何 s3 子命令、head-object，也不得请求 bootstrap 的 skill URL。
if grep -Eiq '(^| )(aws|s3|head-object)( |$)' "$BOOTSTRAP_CALLS"; then echo "FAIL: oc-bootstrap 不得调用 S3/AWS"; fail=1; fi
if grep -Fq 'https://example.invalid/skill.tar' "$BOOTSTRAP_CALLS"; then echo "FAIL: oc-bootstrap 不得下载 skill URL"; fail=1; fi
rm -rf "$BOOTSTRAP_TMP"

if [ "$fail" -eq 0 ]; then echo "unit_test: ALL PASS"; fi
exit "$fail"
