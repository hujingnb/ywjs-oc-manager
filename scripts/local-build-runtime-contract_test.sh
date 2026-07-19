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

# 场景二：普通 runtime 的本地 tag 必须与 local secret 中首个默认镜像精确一致。
dry_run=$(make --no-print-directory -n -C "$repo_root" local-build-runtime)
secret_file=${LOCAL_SECRET_FILE:-"$repo_root/deploy/k8s/local/secret.yaml"}
[[ -f "$secret_file" && -r "$secret_file" ]] || {
  printf '无法读取 local secret runtime 配置\n' >&2
  exit 1
}
# 只读取 hermes.runtime_images 下第一条 ref；遇到同级配置键即停止，禁止误取后续其他镜像。
default_ref_line=$(awk '
  /^      runtime_images:[[:space:]]*$/ { in_runtime_images = 1; next }
  in_runtime_images && /^      [A-Za-z0-9_]+:/ { exit }
  in_runtime_images && /^          ref:/ { print; exit }
' "$secret_file")
[[ "$default_ref_line" =~ ^[[:space:]]*ref:[[:space:]]*\"([^\"]+)\"[[:space:]]*$ ]] || {
  printf 'local secret 首个 runtime ref 缺失或格式错误\n' >&2
  exit 1
}
expected_image=${BASH_REMATCH[1]}
[[ "$expected_image" =~ ^k3d-[A-Za-z0-9][A-Za-z0-9.-]*\.localhost:[0-9]+/oc-manager-aigowork:[A-Za-z0-9_][A-Za-z0-9_.-]*$ ]] || {
  printf 'local secret 首个 runtime ref 不是有效的本地普通 runtime 镜像\n' >&2
  exit 1
}
grep -Fq -- 'override LOCAL_HERMES_IMAGE := $(K3D_REGISTRY_HOST)/oc-manager-aigowork:$(HERMES_VERSION)-dev1' "$repo_root/Makefile" || {
  printf '普通 runtime 本地 tag 未从 HERMES_VERSION 派生\n' >&2
  exit 1
}
grep -Fq -- "-t $expected_image" <<<"$dry_run" || {
  printf '普通 runtime 未构建预期镜像: %s\n' "$expected_image" >&2
  exit 1
}
grep -Fq -- "docker push $expected_image" <<<"$dry_run" || {
  printf '普通 runtime 未推送 local secret 默认镜像: %s\n' "$expected_image" >&2
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

# 场景五：受控替换 local secret 首个 runtime ref 后，内层契约校验必须拒绝配置漂移。
# 递归只执行一次；临时副本避免测试修改仓库内真实开发配置。
if [[ "${LOCAL_RUNTIME_CONTRACT_MUTATION_INNER:-0}" != "1" ]]; then
  mutated_secret=$(mktemp)
  trap 'rm -f "$mutated_secret"' EXIT
  awk '
    /^      runtime_images:/ { in_runtime_images = 1 }
    in_runtime_images && !changed && /^          ref:/ {
      print "          ref: \"k3d-ocm-registry.localhost:5000/oc-manager-aigowork:v9.9.9-dev1\""
      changed = 1
      next
    }
    { print }
    END { if (!changed) exit 2 }
  ' "$repo_root/deploy/k8s/local/secret.yaml" >"$mutated_secret"
  if mutation_output=$(LOCAL_SECRET_FILE="$mutated_secret" LOCAL_RUNTIME_CONTRACT_MUTATION_INNER=1 "$0" 2>&1); then
    printf '普通 runtime 契约测试未识别 local secret 默认镜像漂移\n' >&2
    exit 1
  fi
  grep -Fq -- '普通 runtime 未构建预期镜像:' <<<"$mutation_output" || {
    printf 'local secret 漂移校验因非预期原因失败: %s\n' "$mutation_output" >&2
    exit 1
  }
fi

printf 'local-build 普通 runtime 契约测试通过\n'
