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

# 用 fake Docker 执行真实 Make recipe，既校验平台与目标节点参数，也确保归档和导入
# 任一侧失败都会终止 local-preload，避免纯文本匹配被其他目标或注释中的同文内容绕过。
fake_dir="$(mktemp -d)"
trap 'rm -rf "$fake_dir"' EXIT
cat >"$fake_dir/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

case "${1:-}" in
  image)
    [[ "$*" == "image inspect fixture:latest" ]]
    ;;
  save)
    [[ "$*" == "save --platform linux/amd64 fixture:latest" ]]
    printf 'fake image archive'
    exit "${FAKE_DOCKER_SAVE_EXIT:-0}"
    ;;
  exec)
    [[ "$*" == "exec -i k3d-fixture-server-0 ctr images import -" ]]
    cat >/dev/null
    exit "${FAKE_DOCKER_EXEC_EXIT:-0}"
    ;;
  *)
    exit 64
    ;;
esac
EOF
chmod +x "$fake_dir/docker"

run_local_preload() {
  PATH="$fake_dir:$PATH" make -s --no-print-directory -f "$root/Makefile" local-preload \
    LOCAL_PRELOAD_IMAGES=fixture:latest K3D_CLUSTER=fixture
}

# 正常路径必须完成，以证明测试实际执行的是 local-preload 且参数符合节点直导契约。
run_local_preload >/dev/null

# docker save 位于管道左侧，必须依赖 pipefail 将其失败传递给 Make。
if FAKE_DOCKER_SAVE_EXIT=23 run_local_preload >/dev/null 2>&1; then
  echo "local-preload 掩盖了 docker save 失败" >&2
  exit 1
fi

# 节点 ctr 导入位于管道右侧，其失败同样必须终止 Make。
if FAKE_DOCKER_EXEC_EXIT=24 run_local_preload >/dev/null 2>&1; then
  echo "local-preload 掩盖了 ctr images import 失败" >&2
  exit 1
fi
