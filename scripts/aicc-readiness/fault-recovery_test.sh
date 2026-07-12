#!/usr/bin/env bash
# 验证故障恢复脚本的本地 HTTP 请求不会继承宿主机代理，避免 localhost 被代理错误拦截。
set -euo pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly TARGET="$SCRIPT_DIR/fault-recovery.sh"

# 本地 k3d 的 ocm.localhost 必须直连；所有 curl 调用应通过统一封装显式关闭代理。
if rg -n '^\s*curl\b' "$TARGET" >/dev/null; then
  printf 'FAIL: fault-recovery.sh 存在未封装的 curl 调用\n' >&2
  exit 1
fi

rg -q 'curl --noproxy "\*"' "$TARGET" || {
  printf 'FAIL: fault-recovery.sh 未显式绕过宿主机代理\n' >&2
  exit 1
}

# Hermes 与 manager-api 恢复各需一次续聊，加上基线消息共至少三条访客消息配额。
rg -q 'assert_message_budget' "$TARGET" || {
  printf 'FAIL: fault-recovery.sh 未校验故障演练所需的会话消息配额\n' >&2
  exit 1
}

# 故障演练不提交留资，存在必填字段时必须在重启依赖前终止。
rg -q 'assert_no_required_lead_fields' "$TARGET" || {
  printf 'FAIL: fault-recovery.sh 未校验必填留资字段\n' >&2
  exit 1
}

# manager-api 滚动重启后 Traefik 可能短暂返回非 JSON；消息计数必须通过条件等待读取。
rg -q 'read_session_message_count' "$TARGET" || {
  printf 'FAIL: fault-recovery.sh 未对会话消息计数执行条件等待\n' >&2
  exit 1
}

printf 'PASS: 故障恢复脚本的本地 HTTP 请求显式绕过代理\n'
