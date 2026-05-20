#!/usr/bin/env bash
set -euo pipefail

# Hermes runtime 镜像同步到 runtime node 的本地调试脚本。
# 实现路径与 legacy OpenClaw 时代同源:走 runtime-agent /v1/images/{inspect,load}。
# 默认假设 hermes-runtime:v2026.5.16-dev 已在本机 build(参见 Makefile 的 build-hermes-runtime)。

image="${HERMES_RUNTIME_IMAGE:-hermes-runtime:v2026.5.16-dev}"
agent_url="${RUNTIME_AGENT_FILE_URL:-http://127.0.0.1:7002}"
agent_token="${RUNTIME_AGENT_TOKEN:-}"

validate_hermes_image_ref() {
  local image_ref="$1"
  if [[ "$image_ref" =~ [[:space:]] ]]; then
    echo "Hermes runtime 镜像引用不能包含空白字符:${image_ref}" >&2
    exit 1
  fi
  if [[ "$image_ref" == *@sha256:* ]]; then
    local digest="${image_ref##*@sha256:}"
    if [[ -z "${image_ref%%@sha256:*}" || ! "$digest" =~ ^[0-9a-fA-F]{64}$ ]]; then
      echo "Hermes runtime 镜像 digest 必须是 64 位 sha256 hex:${image_ref}" >&2
      exit 1
    fi
    return
  fi
  local name_part="${image_ref##*/}"
  if [[ "$name_part" != *:* ]]; then
    echo "Hermes runtime 镜像必须固定到具体 tag 或 sha256 digest:${image_ref}" >&2
    exit 1
  fi
  local tag="${image_ref##*:}"
  local lower_tag="${tag,,}"
  case "$lower_tag" in
    main|master|latest|dev|*hermes-main*)
      echo "Hermes runtime 镜像 tag 不能使用浮动或旧 variant:${tag}" >&2
      exit 1
      ;;
  esac
  if [[ ! "$tag" =~ ^v[0-9]+[.][0-9]+[.][0-9]+([._-][A-Za-z0-9_.-]+)?$ ]]; then
    echo "Hermes runtime 镜像 tag 必须以具体 Hermes 版本号开头:${tag}" >&2
    exit 1
  fi
}

validate_hermes_image_ref "$image"

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
