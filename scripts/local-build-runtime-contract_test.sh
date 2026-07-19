#!/usr/bin/env bash
set -euo pipefail

# 从脚本位置解析仓库根目录，保证任意工作目录下都校验同一份 Makefile。
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

# 场景一：local-build 必须显式包含普通 runtime，避免 local-up 只准备控制面和 AICC 镜像。
local_build_rule=$(awk '/^local-build:/ { print; exit }' "$repo_root/Makefile")
case " $local_build_rule " in
  *" local-build-runtime "*) ;;
  *)
    printf 'local-build 缺少 local-build-runtime 依赖: %s\n' "$local_build_rule" >&2
    exit 1
    ;;
esac

# 场景二：普通 runtime 的本地 tag 必须与 local secret 中首选 v0.18.2 镜像精确一致。
dry_run=$(make --no-print-directory -n -C "$repo_root" local-build-runtime)
expected_image='k3d-ocm-registry.localhost:5000/oc-manager-aigowork:v0.18.2-dev1'
grep -Fq -- 'override LOCAL_HERMES_IMAGE := $(K3D_REGISTRY_HOST)/oc-manager-aigowork:$(HERMES_VERSION)-dev1' "$repo_root/Makefile" || {
  printf '普通 runtime 本地 tag 未从 HERMES_VERSION 派生\n' >&2
  exit 1
}
grep -Fq -- "-t $expected_image" <<<"$dry_run" || {
  printf '普通 runtime 未构建预期镜像: %s\n' "$expected_image" >&2
  exit 1
}

# 场景三：镜像必须直接导入 k3d 节点，绕开节点无法解析本地 registry 域名的问题。
grep -Fq -- "docker save --platform linux/amd64 $expected_image | docker exec -i k3d-ocm-server-0 ctr images import -" <<<"$dry_run" || {
  printf '普通 runtime 缺少节点 containerd 直导入步骤\n' >&2
  exit 1
}

# 场景四：注入的共享契约必须在构建命令结束后清理，失败路径也不能污染 variant 目录。
grep -Fq -- 'cp -r runtime/hermes/ocops-contract runtime/hermes/hermes-v0.18.2/ocops-contract' <<<"$dry_run" || {
  printf '普通 runtime 未复用 hermes-inject-contract\n' >&2
  exit 1
}
grep -Fq -- 'rm -rf runtime/hermes/hermes-v0.18.2/ocops-contract runtime/hermes/hermes-v0.18.2/kanban-contract runtime/hermes/hermes-v0.18.2/cron-contract' <<<"$dry_run" || {
  printf '普通 runtime 缺少注入契约清理步骤\n' >&2
  exit 1
}
grep -Fq -- 'docker build  \' <<<"$dry_run" || {
  printf '普通 runtime 缺少受控构建命令\n' >&2
  exit 1
}
grep -Fq -- '|| status=$?;' <<<"$dry_run" || {
  printf '普通 runtime 构建失败时会跳过注入契约清理\n' >&2
  exit 1
}

printf 'local-build 普通 runtime 契约测试通过\n'
