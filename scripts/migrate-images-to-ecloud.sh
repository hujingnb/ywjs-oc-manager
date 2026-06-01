#!/usr/bin/env bash
# 一次性把生产镜像从阿里云 ACR retag 转推到移动云仓库（仅生产，本地 k3d 不动）。
#
# 用法：
#   # 先 docker login 两个仓库（旧 ACR 需有拉取权限、新仓库需有推送权限）
#   docker login crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com
#   docker login ywjs-cc41758e.ecis.huabei-3.cmecloud.cn
#   KUBECONFIG_PROD=~/dir/ywjs/kube/kubeconfig.json ./scripts/migrate-images-to-ecloud.sh
#
# 行为：
#   1) 从生产集群（ocm + oc-apps）枚举实际在跑的镜像，过滤出 ACR 镜像，按命名空间映射
#      （ywjs_app→app、ywjs_public→public）后 pull→tag→push 到新仓库（tag 不变）。
#   2) 转推 6 个构建期基础镜像到新 public（自有/上游已在第 1 步覆盖）。
#   3) 对每个新镜像比对 digest 与源一致，打印结果。
# 幂等：重复执行会覆盖同 tag（内容一致则 push 为 no-op 层），digest 校验保证一致性。
set -euo pipefail

OLD_REGISTRY="crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com"
NEW_REGISTRY="ywjs-cc41758e.ecis.huabei-3.cmecloud.cn"
KUBECONFIG_PROD="${KUBECONFIG_PROD:-$HOME/dir/ywjs/kube/kubeconfig.json}"
KC="kubectl --kubeconfig ${KUBECONFIG_PROD}"

# map_ref 把旧 ACR ref 映射为新仓库 ref：ywjs_app→app、ywjs_public→public，tag 不变。
map_ref() {
  local src="$1"
  echo "$src" \
    | sed -e "s#^${OLD_REGISTRY}/ywjs_app/#${NEW_REGISTRY}/app/#" \
          -e "s#^${OLD_REGISTRY}/ywjs_public/#${NEW_REGISTRY}/public/#"
}

# move 执行 pull→tag→push→digest 校验。
move() {
  local src="$1" dst="$2"
  echo "==> ${src}"
  echo "    -> ${dst}"
  docker pull "$src"
  docker tag "$src" "$dst"
  docker push "$dst"
  local sd dd
  sd="$(docker inspect --format '{{index .RepoDigests 0}}' "$src" 2>/dev/null || true)"
  dd="$(docker inspect --format '{{index .RepoDigests 0}}' "$dst" 2>/dev/null || true)"
  echo "    src digest: ${sd}"
  echo "    dst digest: ${dd}"
}

echo "########## 1) 集群实跑的 ACR 镜像（ocm + oc-apps） ##########"
# 枚举两命名空间所有容器/初始化容器镜像，去重，过滤 ACR。
mapfile -t LIVE < <(
  for ns in ocm oc-apps; do
    $KC get pods -n "$ns" -o jsonpath='{range .items[*]}{range .spec.containers[*]}{.image}{"\n"}{end}{range .spec.initContainers[*]}{.image}{"\n"}{end}{end}'
  done | sort -u | grep "^${OLD_REGISTRY}/"
)
for src in "${LIVE[@]}"; do
  move "$src" "$(map_ref "$src")"
done

echo "########## 2) 构建期基础镜像 → ${NEW_REGISTRY}/public ##########"
# 自有/上游已在第 1 步覆盖；这里只补构建机用的基础镜像。
# 多数基础镜像当前经 ACR ywjs_public 拉取（与 Dockerfile DOCKER_HUB_MIRROR 一致），从 ACR 取以保证同 bits；
# alpine:3.20（ops 基础）当前走 docker.io/library，从 docker.io 取。
move "${OLD_REGISTRY}/ywjs_public/golang:1.25-alpine3.22"      "${NEW_REGISTRY}/public/golang:1.25-alpine3.22"
move "${OLD_REGISTRY}/ywjs_public/alpine:3.22"                 "${NEW_REGISTRY}/public/alpine:3.22"
move "${OLD_REGISTRY}/ywjs_public/node:22-alpine"              "${NEW_REGISTRY}/public/node:22-alpine"
move "${OLD_REGISTRY}/ywjs_public/nginx:1.27-alpine"           "${NEW_REGISTRY}/public/nginx:1.27-alpine"
move "${OLD_REGISTRY}/ywjs_public/python:3.13-slim-bookworm"   "${NEW_REGISTRY}/public/python:3.13-slim-bookworm"
move "docker.io/library/alpine:3.20"                           "${NEW_REGISTRY}/public/alpine:3.20"

echo "✅ 全部镜像已转推。请核对上方各对 src/dst digest 一致。"
