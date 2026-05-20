#!/usr/bin/env bash
set -euo pipefail

# Hermes runtime 镜像本地验证:启动容器跑 hermes --version,确认镜像可用。
# Hermes 没有专用的 verify-install 脚本(legacy OpenClaw 时代有),改用 --version 即可。

image="${HERMES_RUNTIME_IMAGE:-hermes-runtime:v2026.5.16-dev}"

docker run --rm --entrypoint hermes "$image" --version >/dev/null
echo "Hermes runtime 镜像验证通过:${image}"
