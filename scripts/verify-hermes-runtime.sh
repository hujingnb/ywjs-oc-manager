#!/usr/bin/env bash
set -euo pipefail

# Hermes runtime 镜像本地验证:启动容器跑 hermes --version,确认镜像可用。
# Hermes 没有专用的 verify-install 脚本(legacy OpenClaw 时代有),改用 --version 即可。

image="${HERMES_RUNTIME_IMAGE:-hermes-runtime:v2026.5.16-dev}"

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

docker run --rm --entrypoint hermes "$image" --version >/dev/null
echo "Hermes runtime 镜像验证通过:${image}"
