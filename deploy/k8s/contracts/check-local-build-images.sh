#!/usr/bin/env bash
# 校验本地构建后的 manager 镜像直接导入 k3d 节点，避免节点无法解析 Docker 内部 registry 名时滚动更新卡死。
set -euo pipefail

root="${1:-.}"

for manifest in deploy/k8s/local/manager-api.yaml deploy/k8s/local/manager-web.yaml; do
  if ! rg -q 'imagePullPolicy: IfNotPresent' "$root/$manifest"; then
    echo "$manifest 必须使用本地预导入镜像" >&2
    exit 1
  fi
done

for image in oc-manager-api oc-manager-web; do
  if ! rg -Fq "docker save --platform linux/amd64 \$(K3D_REGISTRY_HOST)/$image:dev" "$root/Makefile"; then
    echo "Makefile 未将 $image 导入 k3d 节点" >&2
    exit 1
  fi
done
