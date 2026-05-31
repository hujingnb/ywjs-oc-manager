#!/usr/bin/env bash
# unit_test.sh — oc-lib.sh 纯函数单测，在 ops 镜像容器内跑（容器自带 bash/jq/coreutils）。
# 覆盖：s3_field 解析、creds 过期判断 needs_refresh 两个方向、write_aws_credentials 写出
# session token（STS 临时凭证）与省略 session token（长期凭证直发）两种场景。
set -uo pipefail
# shellcheck source=/usr/local/bin/oc-lib.sh
# shellcheck disable=SC1091
source /usr/local/bin/oc-lib.sh
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

if [ "$fail" -eq 0 ]; then echo "unit_test: ALL PASS"; fi
exit "$fail"
