#!/usr/bin/env bash
set -euo pipefail

base_url="${NEWAPI_BASE_URL:-http://127.0.0.1:3000}"
channel_name="${NEWAPI_OLLAMA_CHANNEL_NAME:-local-ollama}"
channel_base_url="${NEWAPI_OLLAMA_BASE_URL:-http://ollama:11434}"
model="${NEWAPI_OLLAMA_MODEL:-qwen2.5:0.5b}"

curl -fsSI "$base_url" >/dev/null
docker compose exec -T new-api-postgres pg_isready -U root -d new-api >/dev/null

self_use_mode="$(
  docker compose exec -T new-api-postgres \
    psql -U root -d new-api -tAc "select value from options where key = 'SelfUseModeEnabled'"
)"

if [[ "$self_use_mode" != "true" ]]; then
  echo "new-api 未开启自用模式，请先在浏览器中进入「系统设置 → 运营设置」开启自用模式。" >&2
  exit 1
fi

# 调试环境使用 OpenAI 兼容协议接入 Ollama；New API 会自动拼接 /v1，
# 因此渠道 API 地址必须保留为 Ollama 服务根地址，不能写成 /v1 结尾。
channel_count="$(
  docker compose exec -T new-api-postgres \
    psql -U root -d new-api -tAc \
    "select count(*) from channels where name = '${channel_name}' and base_url = '${channel_base_url}' and models like '%${model}%' and status = 1"
)"

if [[ "$channel_count" != "1" ]]; then
  echo "new-api 未找到可用 Ollama 渠道：name=${channel_name}, base_url=${channel_base_url}, model=${model}" >&2
  exit 1
fi

test_time="$(
  docker compose exec -T new-api-postgres \
    psql -U root -d new-api -tAc "select coalesce(test_time, 0) from channels where name = '${channel_name}'"
)"

if [[ "$test_time" == "0" ]]; then
  echo "new-api Ollama 渠道尚未完成浏览器测试，请在渠道管理中点击「测试」。" >&2
  exit 1
fi

echo "new-api HTTP、PostgreSQL、自用模式与 Ollama 渠道验证通过：${base_url}"
