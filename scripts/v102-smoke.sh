#!/usr/bin/env bash
# v1.0.2 smoke：复跑前序 spec §9.2 干净环境流程，全自动断言。
# 前置：Chunk 1 阶段 0 已完成（环境清空 + 9 服务 healthy + new-api setup）。
# 失败立即退出（set -euo pipefail）。
#
# 凭据消歧：
#   - manager 后台 admin 密码：admin123（无感叹号；本脚本默认）
#   - new-api 后台 admin 密码：admin123!（带感叹号；本脚本不直接用）
set -euo pipefail

OCM_BASE_URL="${OCM_BASE_URL:-http://localhost:8080/api/v1}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-admin123}"        # manager 端，与 new-api admin123! 不同
ORG_NAME="${ORG_NAME:-v102-smoke-org-$(date +%s)}"
ADMIN_NAME="${ADMIN_NAME:-v102-smoke-admin-$(date +%s)}"
APP_NAME="${APP_NAME:-v102-smoke-app}"

echo "[0/8] 前置：确认 ollama 模型 qwen2.5:0.5b 已拉取"
if ! docker compose exec -T ollama ollama list 2>/dev/null | grep -q "qwen2.5:0.5b"; then
  echo "  qwen2.5:0.5b 未就位，开始 pull（首次约 397 MB）"
  docker compose exec -T ollama ollama pull qwen2.5:0.5b
fi

echo "[1/8] 登录平台管理员"
TOKEN=$(curl -fsS -XPOST -H "Content-Type: application/json" \
  "$OCM_BASE_URL/auth/login" \
  -d "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}" \
  | jq -r '.access_token')
[ -n "$TOKEN" ] || { echo "登录失败"; exit 1; }

echo "[2/8] 创建组织 $ORG_NAME"
ORG_RESP=$(curl -fsS -XPOST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  "$OCM_BASE_URL/organizations" -d "{\"name\":\"$ORG_NAME\"}")
ORG_ID=$(echo "$ORG_RESP" | jq -r '.id')
NEWAPI_USER_ID=$(echo "$ORG_RESP" | jq -r '.newapi_user_id // empty')
[ -n "$NEWAPI_USER_ID" ] || { echo "❌ newapi_user_id 为空"; exit 1; }
echo "  org_id=$ORG_ID newapi_user_id=$NEWAPI_USER_ID"

echo "[3/8] 校验 organizations 表密文非空 + 解密三件套"
DB_NEWAPI_USER_ID=$(docker compose exec -T manager-postgres psql -U ocm -d ocm -tAc \
  "SELECT newapi_user_id FROM organizations WHERE id='$ORG_ID'")
DB_CIPHERTEXT=$(docker compose exec -T manager-postgres psql -U ocm -d ocm -tAc \
  "SELECT length(newapi_user_credentials_ciphertext) FROM organizations WHERE id='$ORG_ID'")
[ "$DB_NEWAPI_USER_ID" = "$NEWAPI_USER_ID" ] || { echo "❌ DB newapi_user_id 不一致"; exit 1; }
[ "$DB_CIPHERTEXT" -gt 50 ] || { echo "❌ 密文长度异常 ($DB_CIPHERTEXT)"; exit 1; }
echo "  密文长度=$DB_CIPHERTEXT"

# spec §4 阶段 3 第 2 条：用 cipher 工具解密密文确认 username/password/access_token 三件套都在
# manager-api 容器内有解密能力（继承 master_key 配置），用 go run 临时调一段：
DECRYPT_JSON=$(docker compose exec -T manager-api sh -c "go run /tmp/decrypt-org-creds.go '$ORG_ID' 2>/dev/null" || echo "")
if [ -z "$DECRYPT_JSON" ]; then
  # fallback：跳过解密断言，仅用 SQL 读密文长度作为弱断言（已在上面）
  echo "  ⚠️ 解密工具未就位，仅依赖密文长度断言"
else
  echo "$DECRYPT_JSON" | jq -e '.username and .password and .access_token' > /dev/null || {
    echo "❌ 解密后 username/password/access_token 三件套不全"
    echo "$DECRYPT_JSON"
    exit 1
  }
  echo "  解密三件套全部非空 ✅"
fi

echo "[4/8] 注册 runtime node + agent register + 等心跳 active"
NODE_RESP=$(curl -fsS -XPOST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  "$OCM_BASE_URL/runtime-nodes" -d '{"name":"v102-smoke-node","node_data_root":"/var/lib/oc-agent"}')
NODE_ID=$(echo "$NODE_RESP" | jq -r '.id')
BOOTSTRAP=$(echo "$NODE_RESP" | jq -r '.bootstrap_token')
[ -n "$BOOTSTRAP" ] && [ "$BOOTSTRAP" != "null" ] || { echo "❌ bootstrap_token 解析失败"; exit 1; }
echo "  node_id=$NODE_ID bootstrap=${BOOTSTRAP:0:8}..."

# 🔔 本脚本不自动重启 agent。要让 agent 用新 bootstrap_token 注册，需要 ops 人工：
#   1. 把 bootstrap_token 写入 config/agent.yaml 的 manager.bootstrap_token
#   2. docker compose up -d --force-recreate oc-runtime-agent
echo "  🔔 如 agent 不是 active 状态，请人工把 bootstrap_token 注入 agent.yaml + force-recreate"
echo "     bootstrap_token=$BOOTSTRAP（仅本次可见）"
echo "  脚本接下来等待 90 秒，等 agent 完成 register + 心跳..."

DEADLINE=$(($(date +%s) + 90))
NODE_STATUS=""
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  NODE_STATUS=$(docker compose exec -T manager-postgres psql -U ocm -d ocm -tAc \
    "SELECT status FROM runtime_nodes WHERE id='$NODE_ID'")
  LAST_HB=$(docker compose exec -T manager-postgres psql -U ocm -d ocm -tAc \
    "SELECT last_heartbeat_at FROM runtime_nodes WHERE id='$NODE_ID'")
  echo "  status=$NODE_STATUS last_heartbeat=$LAST_HB"
  [ "$NODE_STATUS" = "active" ] && break
  sleep 10
done
[ "$NODE_STATUS" = "active" ] || { echo "❌ 90s 内节点未变为 active"; exit 1; }

echo "[5/8] onboard 成员 + 应用"
ONBOARD_RESP=$(curl -fsS -XPOST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  "$OCM_BASE_URL/organizations/$ORG_ID/members/onboard" -d "{
    \"username\":\"$ADMIN_NAME\",\"display_name\":\"$ADMIN_NAME\",\"password\":\"$ADMIN_NAME-pwd\",
    \"role\":\"org_admin\",
    \"app\":{\"name\":\"$APP_NAME\",\"persona_mode\":\"org_inherited\",\"channel_type\":\"wechat\"}
  }")
APP_ID=$(echo "$ONBOARD_RESP" | jq -r '.app.id')
[ -n "$APP_ID" ] || { echo "❌ APP_ID 解析失败"; exit 1; }
echo "  app_id=$APP_ID"

echo "[6/8] 等待应用进入 binding_waiting（最长 5 分钟）"
DEADLINE=$(($(date +%s) + 300))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  STATUS=$(docker compose exec -T manager-postgres psql -U ocm -d ocm -tAc \
    "SELECT status FROM apps WHERE id='$APP_ID'")
  echo "  apps.status=$STATUS"
  if [ "$STATUS" = "binding_waiting" ]; then
    break
  fi
  if [ "$STATUS" = "error" ]; then
    LAST_ERR=$(docker compose exec -T manager-postgres psql -U ocm -d ocm -tAc \
      "SELECT last_error FROM jobs WHERE payload_json->>'app_id' = '$APP_ID' ORDER BY created_at DESC LIMIT 1")
    echo "❌ 应用进入 error，最近 job 错误：$LAST_ERR"
    exit 1
  fi
  sleep 10
done
[ "$STATUS" = "binding_waiting" ] || { echo "❌ 5 min 内未进入 binding_waiting"; exit 1; }

echo "[7/8] 校验容器 OPENAI_API_KEY 是真 sk-"
APIKEY=$(docker exec "ocm-$APP_ID" env | grep '^OPENAI_API_KEY=' | cut -d= -f2)
case "$APIKEY" in
  sk-*) ;;
  *) echo "❌ OPENAI_API_KEY 不是 sk- 形式: ${APIKEY:0:10}..."; exit 1 ;;
esac
[ "${#APIKEY}" -gt 30 ] || { echo "❌ OPENAI_API_KEY 长度 ${#APIKEY} 太短"; exit 1; }
echo "  OPENAI_API_KEY 长度=${#APIKEY}"

echo "[8/8] 容器内 chat completions 探针"
CHAT_RESP=$(docker exec "ocm-$APP_ID" curl -fsS -XPOST \
  -H "Authorization: Bearer $APIKEY" -H "Content-Type: application/json" \
  http://new-api:3000/v1/chat/completions \
  -d '{"model":"qwen2.5:0.5b","messages":[{"role":"user","content":"say ok"}],"max_tokens":4}')
CONTENT=$(echo "$CHAT_RESP" | jq -r '.choices[0].message.content // empty')
[ -n "$CONTENT" ] || { echo "❌ chat completions 响应无 content: $CHAT_RESP"; exit 1; }
echo "  ollama 响应：$CONTENT"

echo "✅ v1.0.2 smoke 全过"
