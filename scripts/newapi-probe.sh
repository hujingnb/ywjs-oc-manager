#!/usr/bin/env bash
# newapi-probe.sh — 探活 new-api admin API 是否可用，覆盖 manager 真正调用的 4 个端点
# + 用户创建 / 用量日志，共 8 个调用。失败接口数作为脚本退出码。
#
# 入参（环境变量）：
#   NEWAPI_BASE_URL       默认 http://127.0.0.1:3000
#   NEWAPI_ADMIN_TOKEN    必填，从 .env 加载
#
# 输出：
#   - 终端：每个 API 一行 OK/FAIL（不打印 token / 不打印响应体）
#   - 文件：/tmp/newapi-probe-*.json，每个 API 的完整响应体（含 HTTP code 行）
#   - 用户决策：根据失败数量在 docs/superpowers/poc/2026-05-04-newapi-probe.md 决定降级
#
# 退出码：失败的 API 个数（0 = 全部通过）

set -uo pipefail

base_url="${NEWAPI_BASE_URL:-http://127.0.0.1:3000}"
# 防御 .env 写的容器间名（如 http://new-api:3000）：宿主机解析不到，改用 127.0.0.1
if [[ "$base_url" =~ ^https?://new-api(:|/) ]]; then
  base_url="http://127.0.0.1:${NEWAPI_PORT:-3000}"
fi
token="${NEWAPI_ADMIN_TOKEN:-}"
admin_user_id="${NEWAPI_ADMIN_USER_ID:-1}"
ts="$(date +%s)"
# new-api username 有 12 字符上限；用 epoch 后 6 位 + "p" 前缀
probe_user="p${ts: -7}"
tmp_dir="/tmp"

if [[ -z "$token" ]]; then
  echo "FAIL: NEWAPI_ADMIN_TOKEN 未设置；请 source .env 后再运行" >&2
  exit 99
fi

failures=0
declare -a results=()

# call_api <name> <method> <path> [json_body]
# 返回值：把响应写到 /tmp/newapi-probe-<name>.json；2xx 视为 OK
call_api() {
  local name="$1" method="$2" path="$3" body="${4:-}"
  local out="${tmp_dir}/newapi-probe-${name}.json"
  local code
  if [[ -n "$body" ]]; then
    code="$(curl -sS -o "$out" -w '%{http_code}' \
      -X "$method" "${base_url}${path}" \
      -H "Authorization: Bearer ${token}" \
      -H "New-Api-User: ${admin_user_id}" \
      -H 'Content-Type: application/json' \
      --data "$body" || echo '000')"
  else
    code="$(curl -sS -o "$out" -w '%{http_code}' \
      -X "$method" "${base_url}${path}" \
      -H "Authorization: Bearer ${token}" \
      -H "New-Api-User: ${admin_user_id}" || echo '000')"
  fi
  if [[ "$code" =~ ^2 ]]; then
    # 即便 200 也要看 success 字段（new-api 习惯把业务失败编码在 body 里）；
    # jq 必须在 append HTTP_CODE 行之前调用，否则 dump 不再是合法 JSON。
    local success
    success="$(jq -r 'try .success' "$out" 2>/dev/null || echo 'null')"
    # 把 HTTP code 也写进 dump，便于排障
    printf '\n--HTTP_CODE: %s\n' "$code" >> "$out"
    if [[ "$success" == "true" || "$success" == "null" ]]; then
      echo "OK   ${name} (HTTP ${code})"
      results+=("OK|${name}|${code}|${path}")
      return 0
    fi
    local msg
    msg="$(jq -r 'try .message // "no-message"' "$out" 2>/dev/null)"
    echo "FAIL ${name} (HTTP ${code}, success=false, message=${msg})"
    results+=("FAIL|${name}|${code}|${path}|business=${msg}")
    failures=$((failures+1))
    return 1
  fi
  printf '\n--HTTP_CODE: %s\n' "$code" >> "$out"
  echo "FAIL ${name} (HTTP ${code})"
  results+=("FAIL|${name}|${code}|${path}")
  failures=$((failures+1))
  return 1
}

echo "== newapi-probe @ ${base_url} (probe_user=${probe_user}) =="

# 1. CreateUser — 创建临时用户（new-api 标准 admin endpoint）
call_api 1-create-user POST /api/user/ "{
  \"username\": \"${probe_user}\",
  \"password\": \"probe-pass-1234\",
  \"display_name\": \"probe-${ts}\"
}"

# 解析新建 user 的 id（如失败则后续依赖步骤跳过）
new_user_id=""
if [[ -f "${tmp_dir}/newapi-probe-1-create-user.json" ]]; then
  new_user_id="$(jq -r 'try .data.id' "${tmp_dir}/newapi-probe-1-create-user.json" 2>/dev/null || echo '')"
fi

# 部分 new-api 版本 POST /api/user/ 不返回 id；用列表查询 fallback
if [[ -z "$new_user_id" || "$new_user_id" == "null" ]]; then
  curl -sS "${base_url}/api/user/search?keyword=${probe_user}" \
    -H "Authorization: Bearer ${token}" \
    -H "New-Api-User: ${admin_user_id}" \
    -o "${tmp_dir}/newapi-probe-1b-search.json" 2>/dev/null || true
  new_user_id="$(jq -r 'try (.data[0].id) // (.data.items[0].id) // (.data.records[0].id)' "${tmp_dir}/newapi-probe-1b-search.json" 2>/dev/null || echo '')"
fi
# search 也没拿到时再用 admin 列表 fallback：按 username 精确匹配
if [[ -z "$new_user_id" || "$new_user_id" == "null" ]]; then
  curl -sS "${base_url}/api/user/?p=1&page_size=100" \
    -H "Authorization: Bearer ${token}" \
    -H "New-Api-User: ${admin_user_id}" \
    -o "${tmp_dir}/newapi-probe-1c-list.json" 2>/dev/null || true
  new_user_id="$(jq -r --arg u "${probe_user}" 'try ((.data.records // .data.items // .data) | map(select(.username == $u)) | .[0].id)' "${tmp_dir}/newapi-probe-1c-list.json" 2>/dev/null || echo '')"
fi
echo "   probe_user_id=${new_user_id:-<unknown>}"

# 2. GetUserBalance — 查初始余额
if [[ -n "$new_user_id" && "$new_user_id" != "null" ]]; then
  call_api 2-balance-initial GET "/api/user/${new_user_id}"
else
  echo "SKIP 2-balance-initial (没有 user_id)"
  results+=("SKIP|2-balance-initial|-|/api/user/{id}|missing user_id")
  failures=$((failures+1))
fi

# 3. RechargeUser — admin 给 user 加 quota
# new-api 没有 /api/user/recharge endpoint（manager client.go:156 的当前实现在 new-api v1.0.0-alpha.1 上 404）；
# 正确做法是 PUT /api/user/ 把整个 user 对象写回，quota 字段累加。
# 探针走正确路径证明能力存在；manager 端修复留到后续 Chunk。
if [[ -n "$new_user_id" && "$new_user_id" != "null" ]]; then
  curr_user_json="$(curl -sS "${base_url}/api/user/${new_user_id}" \
    -H "Authorization: Bearer ${token}" \
    -H "New-Api-User: ${admin_user_id}")"
  curr_quota="$(echo "$curr_user_json" | jq -r 'try .data.quota // 0')"
  new_quota=$((curr_quota + 1000))
  put_body="$(echo "$curr_user_json" | jq -c --argjson q "$new_quota" '.data | .quota = $q')"
  call_api 3-recharge PUT /api/user/ "$put_body"
else
  echo "SKIP 3-recharge (没有 user_id)"; results+=("SKIP|3-recharge|-|/api/user/|missing user_id"); failures=$((failures+1))
fi

# 4. GetUserBalance（充值后）
if [[ -n "$new_user_id" && "$new_user_id" != "null" ]]; then
  call_api 4-balance-after GET "/api/user/${new_user_id}"
else
  echo "SKIP 4-balance-after"; results+=("SKIP|4-balance-after|-|/api/user/{id}|missing user_id"); failures=$((failures+1))
fi

# 5. CreateAPIKey — 给该 user 建一个 token
new_token_id=""
if [[ -n "$new_user_id" && "$new_user_id" != "null" ]]; then
  call_api 5-create-token POST /api/token/ "{
    \"user_id\": ${new_user_id},
    \"name\": \"probe-token-${ts}\",
    \"models\": [],
    \"remain_quota\": 100,
    \"unlimited_quota\": false,
    \"group\": \"\",
    \"expired_time\": -1,
    \"status\": 1
  }"
  new_token_id="$(jq -r 'try .data.id' "${tmp_dir}/newapi-probe-5-create-token.json" 2>/dev/null || echo '')"
  # POST /api/token/ 不返回 id，按 user_id 列出 token 取最新的
  if [[ -z "$new_token_id" || "$new_token_id" == "null" ]]; then
    curl -sS "${base_url}/api/token/?p=1&size=10" \
      -H "Authorization: Bearer ${token}" \
      -H "New-Api-User: ${admin_user_id}" \
      -o "${tmp_dir}/newapi-probe-5b-list.json" 2>/dev/null || true
    # 当前用户的 token 列表；取 name=probe-token-${ts} 的 id
    new_token_id="$(jq -r --arg n "probe-token-${ts}" 'try ((.data.records // .data.items // .data) | map(select(.name == $n)) | .[0].id)' "${tmp_dir}/newapi-probe-5b-list.json" 2>/dev/null || echo '')"
  fi
else
  echo "SKIP 5-create-token"; results+=("SKIP|5-create-token|-|/api/token/|missing user_id"); failures=$((failures+1))
fi
echo "   probe_token_id=${new_token_id:-<unknown>}"

# 6. DisableAPIKey
if [[ -n "$new_token_id" && "$new_token_id" != "null" ]]; then
  call_api 6-token-disable PUT '/api/token/?status_only=true' "{\"id\": ${new_token_id}, \"status\": 2}"
else
  echo "SKIP 6-token-disable"; results+=("SKIP|6-token-disable|-|/api/token/?status_only=true|missing token_id"); failures=$((failures+1))
fi

# 7. RestoreAPIKey
if [[ -n "$new_token_id" && "$new_token_id" != "null" ]]; then
  call_api 7-token-restore PUT '/api/token/?status_only=true' "{\"id\": ${new_token_id}, \"status\": 1}"
else
  echo "SKIP 7-token-restore"; results+=("SKIP|7-token-restore|-|/api/token/?status_only=true|missing token_id"); failures=$((failures+1))
fi

# 8. UsageLog — 查用量日志（支持的常见 endpoint /api/log/?p=1）
call_api 8-usage-log GET "/api/log/?p=1&page_size=10&user_id=${new_user_id:-0}"

# 清理：删除临时 user 与 token
if [[ -n "$new_token_id" && "$new_token_id" != "null" ]]; then
  curl -sS -X DELETE "${base_url}/api/token/${new_token_id}" \
    -H "Authorization: Bearer ${token}" -H "New-Api-User: ${admin_user_id}" -o /dev/null 2>/dev/null || true
fi
if [[ -n "$new_user_id" && "$new_user_id" != "null" ]]; then
  curl -sS -X DELETE "${base_url}/api/user/${new_user_id}" \
    -H "Authorization: Bearer ${token}" -H "New-Api-User: ${admin_user_id}" -o /dev/null 2>/dev/null || true
fi

echo ""
echo "== Summary =="
printf '%s\n' "${results[@]}"
echo ""
echo "Failed APIs: ${failures}/8"
exit "$failures"
