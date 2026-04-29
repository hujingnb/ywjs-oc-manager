#!/usr/bin/env bash
set -euo pipefail

image="${OPENCLAW_RUNTIME_IMAGE:-openclaw-runtime:dev}"

docker run --rm "$image" /usr/local/bin/openclaw-verify-install
echo "OpenClaw runtime 镜像验证通过：${image}"
