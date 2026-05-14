#!/usr/bin/env bash
set -euo pipefail

# Hermes runtime 镜像同步到 runtime node 的本地调试脚本。
# 实现路径与 legacy OpenClaw 时代同源:走 runtime-agent /v1/images/{inspect,load}。
# 默认假设 hermes-runtime:dev 已在本机 build(参见 Makefile 的 build-hermes-runtime)。

image="${HERMES_RUNTIME_IMAGE:-hermes-runtime:dev}"
agent_url="${RUNTIME_AGENT_FILE_URL:-http://127.0.0.1:7002}"
agent_token="${RUNTIME_AGENT_TOKEN:-}"

auth_args=()
if [[ -n "$agent_token" ]]; then
  auth_args=(-H "Authorization: Bearer ${agent_token}")
fi

local_id="$(docker image inspect "$image" --format '{{.Id}}')"
inspect_url="${agent_url}/v1/images/inspect?image=${image}"

remote_payload="$(curl -fsS "${auth_args[@]}" "$inspect_url")"

# agent 返回的 JSON 很小且字段固定;这里仅用于本地调试脚本判断是否需要传输大镜像。
if grep -Fq "\"id\":\"${local_id}\"" <<<"$remote_payload"; then
  echo "runtime node 已存在匹配 Hermes 镜像,跳过传输:${image} ${local_id}"
  exit 0
fi

echo "runtime node 缺失或镜像 hash 不一致,开始通过 docker save + agent docker load 下发:${image}"
docker save "$image" | curl -fsS "${auth_args[@]}" \
  -H "Content-Type: application/x-tar" \
  --data-binary @- \
  "${agent_url}/v1/images/load?image=${image}" >/dev/null

loaded_payload="$(curl -fsS "${auth_args[@]}" "$inspect_url")"
if ! grep -Fq "\"id\":\"${local_id}\"" <<<"$loaded_payload"; then
  echo "Hermes 镜像下发后 hash 仍不一致:local=${local_id}, remote=${loaded_payload}" >&2
  exit 1
fi

echo "Hermes 镜像已同步到 runtime node:${image} ${local_id}"
