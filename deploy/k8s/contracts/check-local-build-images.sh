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

# 基础镜像也必须限定为节点运行平台后直导 containerd；k3d image import 可能在 ctr
# 报 content digest 缺失时仍返回成功，导致 local-reset 直到部署阶段才回退远端慢拉。
if ! rg -Fq 'docker save --platform linux/amd64 $$img | docker exec -i k3d-$(K3D_CLUSTER)-server-0 ctr images import - || exit 1' "$root/Makefile"; then
  echo "Makefile 的 local-preload 未将 linux/amd64 基础镜像直接导入 k3d 节点" >&2
  exit 1
fi
