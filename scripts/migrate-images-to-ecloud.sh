#!/usr/bin/env bash
# 一次性把生产镜像从旧移动云仓库 retag 转推到新移动云仓库（仅生产，本地 k3d 不动）。
#
# 用法：
#   # 先 docker login 两个仓库（旧仓库需有拉取权限、新仓库需有推送权限）
#   docker login ywjs-cc41758e.ecis.huabei-3.cmecloud.cn
#   docker login ywjs-26257ea5.ecis.huabei-3.cmecloud.cn
#   KUBECONFIG_PROD=~/dir/ywjs/kube/kubeconfig.json ./scripts/migrate-images-to-ecloud.sh
#
# 行为：
#   1) 从生产集群（ocm + oc-apps）和 deploy/k8s/prod/*.yaml 枚举镜像，过滤出旧移动云仓库
#      镜像，保留 app/public 路径与 tag 后 pull→tag→push 到新仓库。
#   2) 转推 6 个构建期基础镜像到新 public（自有/上游已在第 1 步覆盖）。
#   3) 对每个新镜像打印源/目标 digest，供迁移后人工核对。
# 幂等：重复执行会覆盖同 tag；内容一致时 push 通常为 no-op 层。
set -euo pipefail

OLD_REGISTRY="ywjs-cc41758e.ecis.huabei-3.cmecloud.cn"
NEW_REGISTRY="ywjs-26257ea5.ecis.huabei-3.cmecloud.cn"
KUBECONFIG_PROD="${KUBECONFIG_PROD:-$HOME/dir/ywjs/kube/kubeconfig.json}"
KC="kubectl --kubeconfig ${KUBECONFIG_PROD}"

# map_ref 只替换 registry 域名，保留 app/public 路径与 tag，避免迁移时改变发布版本。
map_ref() {
  local src="$1"
  echo "${src/#${OLD_REGISTRY}\//${NEW_REGISTRY}/}"
}

# digest_for 从本机镜像的 RepoDigests 中筛出当前 repository 对应的 digest。
# 同一个 image ID 可能同时有阿里云、旧移动云、新移动云多个 tag/digest，只取当前 ref
# 对应的 repository，避免核对输出被历史 tag 干扰。
digest_for() {
  local ref="$1"
  local repo="${ref%@*}"
  repo="${repo%:*}"
  docker inspect --format '{{range .RepoDigests}}{{println .}}{{end}}' "$ref" 2>/dev/null \
    | grep -m1 "^${repo}@" || true
}

# move 执行 pull/cache→tag→push，并打印 digest 供迁移后核对。
move() {
  local src="$1" dst="$2"
  echo "==> ${src}"
  echo "    -> ${dst}"
  if docker image inspect "$src" >/dev/null 2>&1; then
    echo "    source: 使用本机 Docker 缓存"
  else
    # 旧仓库仍可访问时从远端补齐；若旧仓库已不可用，这里会失败并暴露缺失的具体 tag。
    docker pull "$src"
  fi
  docker tag "$src" "$dst"
  docker push "$dst"
  local sd dd
  sd="$(digest_for "$src")"
  dd="$(digest_for "$dst")"
  echo "    src digest: ${sd}"
  echo "    dst digest: ${dd}"
}

# collect_live_refs 枚举两命名空间所有容器/初始化容器镜像；只读访问生产集群，不改集群状态。
collect_live_refs() {
  for ns in ocm oc-apps; do
    $KC get pods -n "$ns" -o jsonpath='{range .items[*]}{range .spec.containers[*]}{.image}{"\n"}{end}{range .spec.initContainers[*]}{.image}{"\n"}{end}{end}'
  done
}

# collect_manifest_refs 补充生产 YAML/本地 secret.yaml 中尚未被当前 Pod 使用的镜像，
# 例如 hermes/ops 配置镜像，避免新仓库空仓时后续新建 app pod 拉不到镜像。
collect_manifest_refs() {
  local file
  local files=()
  local registry_re
  registry_re="${OLD_REGISTRY//./\\.}"
  for file in deploy/k8s/prod/*.yaml; do
    [ -e "$file" ] || continue
    # secret.example.yaml 只放模板和占位 tag，不代表需要迁移的真实生产镜像。
    [ "$file" = "deploy/k8s/prod/secret.example.yaml" ] && continue
    files+=("$file")
  done
  if [ "${#files[@]}" -gt 0 ]; then
    grep -hEo "${registry_re}/[^\"'[:space:]]+" "${files[@]}" || true
  fi
}

echo "########## 1) 生产实跑 + manifest 中的旧移动云镜像 ##########"
mapfile -t IMAGES < <(
  {
    collect_live_refs
    collect_manifest_refs
  } | sort -u | grep "^${OLD_REGISTRY}/" || true
)
for src in "${IMAGES[@]}"; do
  move "$src" "$(map_ref "$src")"
done

echo "########## 2) 构建期基础镜像 → ${NEW_REGISTRY}/public ##########"
# 自有/上游已在第 1 步覆盖；这里只补构建机用的基础镜像，全部从旧移动云 public 取，
# 避免迁移期间重新依赖 docker.io 或历史阿里云 ACR。
move "${OLD_REGISTRY}/public/golang:1.25-alpine3.22"      "${NEW_REGISTRY}/public/golang:1.25-alpine3.22"
move "${OLD_REGISTRY}/public/alpine:3.22"                 "${NEW_REGISTRY}/public/alpine:3.22"
move "${OLD_REGISTRY}/public/node:22-alpine"              "${NEW_REGISTRY}/public/node:22-alpine"
move "${OLD_REGISTRY}/public/nginx:1.27-alpine"           "${NEW_REGISTRY}/public/nginx:1.27-alpine"
move "${OLD_REGISTRY}/public/python:3.13-slim-bookworm"   "${NEW_REGISTRY}/public/python:3.13-slim-bookworm"
move "${OLD_REGISTRY}/public/alpine:3.20"                 "${NEW_REGISTRY}/public/alpine:3.20"

echo "✅ 全部镜像已转推。请核对上方各对 src/dst digest。"
