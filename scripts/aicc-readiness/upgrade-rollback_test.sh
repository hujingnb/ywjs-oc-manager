#!/usr/bin/env bash
# 验证升级演练在访问 *.localhost 前已准备本地 Ingress controller。
set -euo pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly TARGET="$SCRIPT_DIR/upgrade-rollback.sh"

# k3s 直拉 Docker Hub 可能长期阻塞；Traefik 必须与业务基础镜像一起导入节点。
rg -q 'rancher/mirrored-library-traefik:2\.11\.18' "$TARGET" || {
  printf 'FAIL: upgrade-rollback.sh 未预加载固定 Traefik 镜像\n' >&2
  exit 1
}

# 原始 Docker Hub 在本地网络不可达，必须从固定国内镜像源拉取后 retag。
rg -q 'registry\.cn-hangzhou\.aliyuncs\.com/rancher/mirrored-library-traefik:2\.11\.18' "$TARGET" || {
  printf 'FAIL: upgrade-rollback.sh 未使用国内 Traefik 镜像源\n' >&2
  exit 1
}

# newapi.localhost/RAGFlow 初始化依赖 Ingress，不能只等待业务 Deployment Ready。
rg -q 'rollout status deploy/traefik' "$TARGET" || {
  printf 'FAIL: upgrade-rollback.sh 未等待 Traefik Ready\n' >&2
  exit 1
}

printf 'PASS: 升级演练会预加载并等待 Traefik Ready\n'
