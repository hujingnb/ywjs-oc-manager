#!/usr/bin/env bash
set -euo pipefail

base_url="${OLLAMA_BASE_URL:-http://127.0.0.1:11434}"
model="${OLLAMA_DEBUG_MODEL:-qwen2.5:0.5b}"
pull_timeout="${OLLAMA_PULL_TIMEOUT_SECONDS:-900}"

pull_model() {
  local attempt="$1"

  echo "开始拉取 Ollama 调试模型：${model}（第 ${attempt} 次，超时 ${pull_timeout}s）"
  timeout "${pull_timeout}" docker exec ollama ollama pull "$model"
}

curl -fsS "${base_url}/api/tags" >/dev/null
echo "Ollama API 可访问：${base_url}"

if [[ "${OLLAMA_SKIP_MODEL_PULL:-0}" == "1" ]]; then
  echo "已跳过模型拉取；设置 OLLAMA_SKIP_MODEL_PULL=0 可执行完整生成验证。"
  exit 0
fi

# Docker Hub、Ollama Registry 等外部网络在本地调试时偶发中断，因此失败后只重试一次，
# 既满足可恢复性，也避免持续重试掩盖网络或镜像源问题。
if ! pull_model 1; then
  echo "Ollama 模型拉取失败，准备重试一次：${model}" >&2
  pull_model 2
fi

curl -fsS "${base_url}/api/generate" \
  -H "Content-Type: application/json" \
  -d "{\"model\":\"${model}\",\"prompt\":\"请只回复 ok\",\"stream\":false}" \
  | grep -q '"response"'

echo "Ollama 模型调用验证通过：${model}"
