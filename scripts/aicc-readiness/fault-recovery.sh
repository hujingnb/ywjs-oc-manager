#!/usr/bin/env bash
# 本脚本只针对本仓库的本地 k3d 环境。它按顺序注入单个故障，并在退出时恢复原副本数。
set -euo pipefail

readonly OCM_NAMESPACE="ocm"
readonly APPS_NAMESPACE="oc-apps"
readonly LOCAL_CONTEXT="k3d-ocm"
readonly BASE_URL_DEFAULT="http://ocm.localhost"
readonly WAIT_TIMEOUT="300s"

BASE_URL="${AICC_BASE_URL:-$BASE_URL_DEFAULT}"
PUBLIC_TOKEN="${AICC_PUBLIC_TOKEN:-}"
DRY_RUN=false
RUN_STARTED=false
SESSION_TOKEN=""
APP_ID="${AICC_APP_ID:-}"

MANAGER_REPLICAS=""
RAGFLOW_REPLICAS=""
NEW_API_REPLICAS=""
REDIS_REPLICAS=""
MYSQL_REPLICAS=""

usage() {
  cat <<'EOF'
用法：
  AICC_PUBLIC_TOKEN=<活跃公开链接 token> bash scripts/aicc-readiness/fault-recovery.sh [--dry-run]

可选环境变量：
  AICC_BASE_URL  本地 manager 地址（默认 http://ocm.localhost）
  AICC_APP_ID    客服 runtime 的 app ID；未设置时从本地 MySQL 按公开 token 查询

脚本仅接受 k3d-ocm context，只操作 ocm 与 oc-apps namespace。它会重启 manager-api 和
指定客服 runtime Pod，并依次将 ragflow、new-api、redis、mysql 缩容为 0 后恢复。
--dry-run 只输出将执行的操作，不修改 Kubernetes 资源。
EOF
}

log() {
  printf '%s %s\n' "$(date '+%H:%M:%S')" "$*"
}

die() {
  log "FAIL: $*" >&2
  exit 1
}

run() {
  if "$DRY_RUN"; then
    printf 'DRY-RUN: '
    printf '%q ' "$@"
    printf '\n'
    return 0
  fi
  "$@"
}

curl_local() {
  # 本地 k3d 的 ocm.localhost 不能经过宿主机代理；代理返回 502 会掩盖真实依赖故障。
  command curl --noproxy "*" "$@"
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "缺少命令：$1"
}

http_status() {
  # 故障注入期间网络连接失败也作为可观测的不可用状态返回 000。
  curl_local --connect-timeout 5 --max-time 40 --silent --show-error --output /tmp/aicc-readiness-body.$$ \
    --write-out '%{http_code}' "$@" || true
}

expect_success() {
  local label="$1"
  shift
  local status
  status="$(http_status "$@")"
  [[ "$status" =~ ^2 ]] || die "$label 返回 HTTP $status，响应：$(tr '\n' ' ' </tmp/aicc-readiness-body.$$)"
  log "PASS: $label (HTTP $status)"
}

expect_dependency_failure() {
  local label="$1"
  shift
  local status
  status="$(http_status "$@")"
  if [[ "$status" == "000" || "$status" =~ ^5 ]]; then
    log "PASS: $label 在故障期间不可用 (HTTP $status)"
    return 0
  fi
  die "$label 在依赖故障期间仍返回 HTTP $status；拒绝把未触达依赖的检查记为通过"
}

expect_dependency_degraded_reply() {
  local label="$1" status text code client_message_id
  client_message_id="$(uuidgen | tr '[:upper:]' '[:lower:]')"
  status="$(http_status -H 'Content-Type: application/json' \
    --data "$(jq -nc --arg text "故障演练安全降级 $(date +%s)" --arg client_message_id "$client_message_id" '{text: $text, client_message_id: $client_message_id}')" \
    "$(session_url)/messages")"
  if [[ "$status" =~ ^2 ]]; then
    text="$(jq -er '.message.text' /tmp/aicc-readiness-body.$$ 2>/dev/null || true)"
    [[ -n "$text" ]] || die "$label 的降级回复为空或不是有效 JSON"
  elif [[ "$status" =~ ^5 ]]; then
    code="$(jq -er '.code' /tmp/aicc-readiness-body.$$ 2>/dev/null || true)"
    text="$(jq -er '.message' /tmp/aicc-readiness-body.$$ 2>/dev/null || true)"
    [[ -n "$code" && -n "$text" ]] || die "$label 未返回稳定 JSON 错误，HTTP $status"
  else
    die "$label 返回非预期 HTTP $status：$(tr '\n' ' ' </tmp/aicc-readiness-body.$$)"
  fi
  if grep -Eqi 'api call failed|connection error|dial tcp|traceback|stack trace|upstream' <<<"$text"; then
    die "$label 向访客泄露了原始上游错误：$text"
  fi
  log "PASS: $label 返回安全响应且未泄露原始错误 (HTTP $status)"
}

capture_replicas() {
  MANAGER_REPLICAS="$(kubectl -n "$OCM_NAMESPACE" get deploy/manager-api -o jsonpath='{.spec.replicas}')"
  RAGFLOW_REPLICAS="$(kubectl -n "$OCM_NAMESPACE" get deploy/ragflow -o jsonpath='{.spec.replicas}')"
  NEW_API_REPLICAS="$(kubectl -n "$OCM_NAMESPACE" get deploy/new-api -o jsonpath='{.spec.replicas}')"
  REDIS_REPLICAS="$(kubectl -n "$OCM_NAMESPACE" get statefulset/redis -o jsonpath='{.spec.replicas}')"
  MYSQL_REPLICAS="$(kubectl -n "$OCM_NAMESPACE" get statefulset/mysql -o jsonpath='{.spec.replicas}')"
}

restore() {
  local status=$?
  [[ "$RUN_STARTED" == true ]] || return "$status"
  log "恢复本次演练前的副本数"
  set +e
  kubectl -n "$OCM_NAMESPACE" scale deploy/manager-api --replicas="$MANAGER_REPLICAS"
  kubectl -n "$OCM_NAMESPACE" scale deploy/ragflow --replicas="$RAGFLOW_REPLICAS"
  kubectl -n "$OCM_NAMESPACE" scale deploy/new-api --replicas="$NEW_API_REPLICAS"
  kubectl -n "$OCM_NAMESPACE" scale statefulset/redis --replicas="$REDIS_REPLICAS"
  kubectl -n "$OCM_NAMESPACE" scale statefulset/mysql --replicas="$MYSQL_REPLICAS"
  kubectl -n "$OCM_NAMESPACE" rollout status deploy/manager-api --timeout="$WAIT_TIMEOUT"
  kubectl -n "$OCM_NAMESPACE" rollout status deploy/ragflow --timeout="$WAIT_TIMEOUT"
  kubectl -n "$OCM_NAMESPACE" rollout status deploy/new-api --timeout="$WAIT_TIMEOUT"
  kubectl -n "$OCM_NAMESPACE" rollout status statefulset/redis --timeout="$WAIT_TIMEOUT"
  kubectl -n "$OCM_NAMESPACE" rollout status statefulset/mysql --timeout="$WAIT_TIMEOUT"
  rm -f /tmp/aicc-readiness-body.$$
  return "$status"
}

wait_deployment() {
  run kubectl -n "$OCM_NAMESPACE" rollout status "deploy/$1" --timeout="$WAIT_TIMEOUT"
}

wait_statefulset() {
  run kubectl -n "$OCM_NAMESPACE" rollout status "statefulset/$1" --timeout="$WAIT_TIMEOUT"
}

session_url() {
  printf '%s/api/v1/public/aicc/sessions/%s' "$BASE_URL" "$SESSION_TOKEN"
}

check_session_restored() {
  expect_success "公开会话可恢复" "$(session_url)"
}

read_session_message_count() {
  local status count attempts=0
  while (( attempts < 30 )); do
    status="$(http_status "$(session_url)")"
    if [[ "$status" =~ ^2 ]]; then
      count="$(jq -er '.session.messages | length' /tmp/aicc-readiness-body.$$ 2>/dev/null || true)"
      if [[ "$count" =~ ^[0-9]+$ ]]; then
        printf '%s' "$count"
        return 0
      fi
    fi
    attempts=$((attempts + 1))
    log "等待公开会话返回有效消息列表（第 $attempts/30 次，HTTP $status）" >&2
    sleep 1
  done
  die "公开会话在恢复后未返回有效消息列表"
}

create_session() {
  local response status
  response="$(mktemp)"
  status="$(curl_local --connect-timeout 5 --max-time 40 --silent --show-error --output "$response" --write-out '%{http_code}' \
    -H 'Content-Type: application/json' \
    --data '{"channel":"web_link","source_url":"http://ocm.localhost/aicc-readiness"}' \
    "$BASE_URL/api/v1/public/aicc/agents/$PUBLIC_TOKEN/sessions")"
  [[ "$status" == "201" ]] || die "创建故障演练会话失败，HTTP $status：$(tr '\n' ' ' <"$response")"
  SESSION_TOKEN="$(jq -r '.session.session_token // empty' "$response")"
  rm -f "$response"
  [[ -n "$SESSION_TOKEN" ]] || die "创建会话响应缺少 session_token"
  log "PASS: 创建可续接的公开会话"
}

send_message() {
  local label="$1" text="$2" before after status response attempts=0 client_message_id
  before="$(read_session_message_count)"
  response="$(mktemp)"
  client_message_id="$(uuidgen | tr '[:upper:]' '[:lower:]')"
  while :; do
    status="$(curl_local --connect-timeout 5 --max-time 90 --silent --show-error --output "$response" --write-out '%{http_code}' \
      -H 'Content-Type: application/json' --data "$(jq -nc --arg text "$text" --arg client_message_id "$client_message_id" '{text: $text, client_message_id: $client_message_id}')" \
      "$(session_url)/messages" || true)"
    # Kubernetes Ready 只代表三个容器已就绪；Hermes 仍可能在恢复 S3 状态。仅对明确的
    # 启动窗口错误重试，其他响应立即失败，避免把业务错误或重复写入掩盖为恢复成功。
    if [[ "$status" == "503" ]] && jq -e '.code == "RUNTIME_NOT_AVAILABLE"' "$response" >/dev/null 2>&1 && (( attempts < 60 )); then
      attempts=$((attempts + 1))
      log "$label 等待 Hermes 恢复（第 $attempts/60 次，HTTP 503）"
      sleep 5
      continue
    fi
    break
  done
  [[ "$status" =~ ^2 ]] || die "$label 发送消息失败，HTTP $status：$(tr '\n' ' ' <"$response")"
  rm -f "$response"
  after="$(read_session_message_count)"
  [[ "$after" -eq $((before + 2)) ]] || die "$label 消息数异常：发送前=$before，发送后=$after（预期恰好增加访客和助手各一条）"
  log "PASS: $label 续聊成功且没有重复消息"
}

message_request_args() {
  jq -nc --arg text "故障演练依赖检查 $(date +%s)" '{text: $text}'
}

assert_local_environment() {
  [[ "$(kubectl config current-context)" == "$LOCAL_CONTEXT" ]] || die "当前 context 不是 $LOCAL_CONTEXT，拒绝执行"
  [[ "$BASE_URL" == http://ocm.localhost* ]] || die "AICC_BASE_URL 必须是本地 ocm.localhost 地址"
  kubectl -n "$OCM_NAMESPACE" get deploy/manager-api deploy/ragflow deploy/new-api >/dev/null
  kubectl -n "$OCM_NAMESPACE" get statefulset/redis statefulset/mysql >/dev/null
  [[ "$PUBLIC_TOKEN" =~ ^[A-Za-z0-9_-]{20,}$ ]] || die "AICC_PUBLIC_TOKEN 格式非法"
}

resolve_app_id() {
  if [[ -n "$APP_ID" ]]; then
    return
  fi
  APP_ID="$(kubectl -n "$OCM_NAMESPACE" exec mysql-0 -- sh -c \
    "mysql -uroot -p\"\$MYSQL_ROOT_PASSWORD\" -N -e \"USE ocm; SELECT app_id FROM aicc_agents WHERE public_token='${PUBLIC_TOKEN}' AND status='active' AND deleted_at IS NULL LIMIT 1;\"" 2>/dev/null)"
  [[ -n "$APP_ID" ]] || die "未找到活跃公开 token 对应的 AICC app；请提供 AICC_APP_ID"
  [[ "$APP_ID" =~ ^[0-9a-f-]{36}$ ]] || die "AICC_APP_ID 格式非法"
}

assert_message_budget() {
  local limit
  # 基线、Hermes 重启和 manager-api 重启各需要一次成功续聊；先校验避免重启后才因业务配额中断。
  limit="$(kubectl -n "$OCM_NAMESPACE" exec mysql-0 -- sh -c \
    "mysql -uroot -p\"\$MYSQL_ROOT_PASSWORD\" -N -e \"USE ocm; SELECT COALESCE((SELECT message_limit_per_session FROM aicc_agent_settings WHERE agent_id = (SELECT id FROM aicc_agents WHERE public_token='${PUBLIC_TOKEN}' LIMIT 1)), 100);\"" 2>/dev/null)"
  [[ "$limit" =~ ^[0-9]+$ ]] || die "无法读取故障演练智能体的会话消息上限"
  (( limit >= 3 )) || die "会话消息上限为 $limit，故障演练至少需要 3 条；请选择默认或更高上限的测试智能体"
  log "PASS: 会话消息上限满足故障演练（$limit >= 3）"
}

assert_no_required_lead_fields() {
  local required_count
  # 故障演练只验证会话与依赖恢复，不提交访客留资；必填字段会改变首条消息的业务前置条件。
  required_count="$(kubectl -n "$OCM_NAMESPACE" exec mysql-0 -- sh -c \
    "mysql -uroot -p\"\$MYSQL_ROOT_PASSWORD\" -N -e \"USE ocm; SELECT COUNT(*) FROM aicc_lead_fields WHERE agent_id = (SELECT id FROM aicc_agents WHERE public_token='${PUBLIC_TOKEN}' LIMIT 1) AND required = 1 AND deleted_at IS NULL;\"" 2>/dev/null)"
  [[ "$required_count" =~ ^[0-9]+$ ]] || die "无法读取故障演练智能体的留资字段"
  (( required_count == 0 )) || die "智能体存在 $required_count 个必填留资字段；请选择无需留资即可发送消息的测试智能体"
  log "PASS: 智能体无必填留资字段"
}

restart_runtime_and_manager() {
  local runtime_pod manager_pod
  runtime_pod="$(kubectl -n "$APPS_NAMESPACE" get pods -l "app=$APP_ID" -o jsonpath='{.items[0].metadata.name}')"
  [[ -n "$runtime_pod" ]] || die "未找到 app=$APP_ID 的客服 runtime Pod"
  run kubectl -n "$APPS_NAMESPACE" delete pod "$runtime_pod" --wait=true
  run kubectl -n "$APPS_NAMESPACE" wait --for=condition=Ready pod -l "app=$APP_ID" --timeout="$WAIT_TIMEOUT"
  check_session_restored
  send_message "Hermes runtime 重启后" "故障演练：Hermes 重启后续聊 $(date +%s)"

  manager_pod="$(kubectl -n "$OCM_NAMESPACE" get pods -l app=manager-api -o jsonpath='{.items[0].metadata.name}')"
  [[ -n "$manager_pod" ]] || die "未找到 manager-api Pod"
  run kubectl -n "$OCM_NAMESPACE" delete pod "$manager_pod" --wait=true
  wait_deployment manager-api
  check_session_restored
  send_message "manager-api 重启后" "故障演练：manager 重启后续聊 $(date +%s)"
}

test_dependency() {
  local kind="$1" name="$2" resource="$3" restore_replicas="$4"
  log "注入 $name 单点故障"
  run kubectl -n "$OCM_NAMESPACE" scale "$kind/$resource" --replicas=0
  run kubectl -n "$OCM_NAMESPACE" wait --for=delete pod -l "app=$resource" --timeout="$WAIT_TIMEOUT"
  # 公开创建会话必经 Redis；消息路径会经过 runtime、RAGFlow/new-api；MySQL 是会话持久层。
  if [[ "$resource" == "redis" ]]; then
    expect_dependency_failure "$name 下创建会话" -H 'Content-Type: application/json' --data '{"channel":"web_link"}' \
      "$BASE_URL/api/v1/public/aicc/agents/$PUBLIC_TOKEN/sessions"
  elif [[ "$resource" == "ragflow" || "$resource" == "new-api" ]]; then
    expect_dependency_degraded_reply "$name 故障"
  else
    expect_dependency_failure "$name 下读取既有会话" "$(session_url)"
  fi
  run kubectl -n "$OCM_NAMESPACE" scale "$kind/$resource" --replicas="$restore_replicas"
  if [[ "$kind" == "deploy" ]]; then wait_deployment "$resource"; else wait_statefulset "$resource"; fi
  check_session_restored
  log "PASS: $name 恢复后公开会话可访问"
}

main() {
  case "${1:-}" in
    --help|-h) usage; return 0 ;;
    --dry-run) DRY_RUN=true ;;
    '') ;;
    *) die "未知参数：$1（使用 --help 查看说明）" ;;
  esac
  "$DRY_RUN" && { usage; log "DRY-RUN: 不会修改环境"; return 0; }
  [[ -n "$PUBLIC_TOKEN" ]] || die "必须设置 AICC_PUBLIC_TOKEN"
  require_command kubectl
  require_command curl
  require_command jq
  assert_local_environment
  assert_message_budget
  assert_no_required_lead_fields
  capture_replicas
  RUN_STARTED=true
  trap restore EXIT INT TERM

  expect_success "公开配置基线" "$BASE_URL/api/v1/public/aicc/agents/$PUBLIC_TOKEN/config"
  create_session
  resolve_app_id
  check_session_restored
  send_message "基线" "故障演练：基线续聊 $(date +%s)"
  restart_runtime_and_manager
  test_dependency deploy "RAGFlow" ragflow "$RAGFLOW_REPLICAS"
  test_dependency deploy "new-api" new-api "$NEW_API_REPLICAS"
  test_dependency statefulset "Redis" redis "$REDIS_REPLICAS"
  test_dependency statefulset "MySQL" mysql "$MYSQL_REPLICAS"
  log "PASS: 所有故障注入与恢复检查完成"
}

main "$@"
