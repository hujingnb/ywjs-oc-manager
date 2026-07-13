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

# 原始 Docker Hub 在本地网络不可达，必须从固定 DaoCloud 镜像源拉取后 retag。
rg -q 'docker\.m\.daocloud\.io/rancher/mirrored-library-traefik:2\.11\.18' "$TARGET" || {
  printf 'FAIL: upgrade-rollback.sh 未使用 DaoCloud Traefik 镜像源\n' >&2
  exit 1
}

# k3s 的 Traefik Helm job 同样不能回退直连 Docker Hub。
rg -q 'docker\.m\.daocloud\.io/rancher/klipper-helm:v0\.9\.3-build20241008' "$TARGET" || {
  printf 'FAIL: upgrade-rollback.sh 未预加载 DaoCloud klipper-helm 镜像\n' >&2
  exit 1
}

# onnxruntime-node 默认下载 CUDA 依赖时会直连 GitHub；Web 不使用 CUDA，演练必须显式跳过。
rg -q 'ONNXRUNTIME_NODE_INSTALL_CUDA=skip' "$TARGET" || {
  printf 'FAIL: upgrade-rollback.sh 未跳过 onnxruntime CUDA 下载\n' >&2
  exit 1
}

# 临时验证副本不携带 node_modules，浏览器冒烟前必须自行安装 Playwright 及前端依赖。
rg -q 'npm --prefix "\$REPO_ROOT/web" ci --no-audit --no-fund' "$TARGET" || {
  printf 'FAIL: upgrade-rollback.sh 未为浏览器冒烟安装前端依赖\n' >&2
  exit 1
}

# newapi.localhost/RAGFlow 初始化依赖 Ingress，不能只等待业务 Deployment Ready。
rg -q 'rollout status deploy/traefik' "$TARGET" || {
  printf 'FAIL: upgrade-rollback.sh 未等待 Traefik Ready\n' >&2
  exit 1
}

# API 与 Web 任一组件滚动失败都必须判定整次镜像切换失败，不能由后一条成功命令覆盖返回码。
rg -q 'local api_status=0 web_status=0' "$TARGET" &&
  rg -q 'return 1' "$TARGET" || {
  printf 'FAIL: upgrade-rollback.sh 未聚合 API 与 Web 的 rollout 结果\n' >&2
  exit 1
}

# 基线 SQL 不能叠加导入到新版 schema；必须先重建数据库，避免残留外键或索引冲突。
rg -q "DROP DATABASE IF EXISTS ocm; CREATE DATABASE ocm" "$TARGET" || {
  printf 'FAIL: upgrade-rollback.sh 恢复基线前未重建 ocm 数据库\n' >&2
  exit 1
}

printf 'PASS: 升级演练会预加载并等待 Traefik Ready\n'
