#!/usr/bin/env bash
# AICC 本地升级/回滚演练。仅允许在 k3d-ocm 的 ocm/oc-apps 范围内运行，绝不用于生产集群。
#
# 演练会重建本地 k3d 数据，因此执行前会备份当前 ocm 库到 /tmp；最终始终恢复到 KEFU_SHA
# 镜像。源码通过 git archive 导出到临时目录构建，既不切换分支，也不会改写工作区。
set -euo pipefail

readonly REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly CLUSTER_NAME="${K3D_CLUSTER:-ocm}"
readonly KUBE_CONTEXT="k3d-${CLUSTER_NAME}"
readonly NAMESPACE="ocm"
readonly REGISTRY_HOST="${K3D_REGISTRY_HOST:-k3d-ocm-registry.localhost:5000}"
readonly CURRENT_BACKUP="${AICC_READINESS_CURRENT_BACKUP:-/tmp/aicc-readiness-pre-drill.sql}"
readonly BASELINE_BACKUP="${AICC_READINESS_BACKUP:-/tmp/aicc-readiness-backup.sql}"
readonly RUN_BROWSER_SMOKE="${AICC_READINESS_RUN_BROWSER_SMOKE:-1}"
readonly ROLLOUT_TIMEOUT="${AICC_READINESS_ROLLOUT_TIMEOUT:-900s}"
readonly LOCAL_OPS_IMAGE="${REGISTRY_HOST}/oc-manager-ops:dev2"
readonly LOCAL_HERMES_IMAGE="${REGISTRY_HOST}/oc-manager-aigowork:v2026.7.1-dev1"
readonly TRAEFIK_IMAGE="rancher/mirrored-library-traefik:2.11.18"
readonly TRAEFIK_MIRROR_IMAGE="docker.m.daocloud.io/rancher/mirrored-library-traefik:2.11.18"
readonly KLIPPER_HELM_IMAGE="rancher/klipper-helm:v0.9.3-build20241008"
readonly KLIPPER_HELM_MIRROR_IMAGE="docker.m.daocloud.io/rancher/klipper-helm:v0.9.3-build20241008"

MASTER_SHA=""
KEFU_SHA=""
MASTER_API_IMAGE=""
MASTER_WEB_IMAGE=""
KEFU_API_IMAGE=""
KEFU_WEB_IMAGE=""
TEMP_DIR=""
FINAL_KEFU_DEPLOYED=0

log() {
  printf '\n==> %s\n' "$*"
}

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "缺少命令: $1"
}

kubectl_ocm() {
  kubectl --context "$KUBE_CONTEXT" -n "$NAMESPACE" "$@"
}

require_local_environment() {
  [[ "$(git -C "$REPO_ROOT" branch --show-current)" == "kefu" ]] || fail "必须从 kefu 分支运行"
  [[ -z "$(git -C "$REPO_ROOT" status --porcelain)" ]] || fail "工作区必须干净，拒绝覆盖未提交改动"
  [[ "$(kubectl config current-context)" == "$KUBE_CONTEXT" ]] || fail "当前 kubectl context 必须是 $KUBE_CONTEXT"
  # 演练可从上次中断后仅剩控制面的 k3d 集群恢复；已有 MySQL 时才执行原环境备份。
}

mysql_dump() {
  local output="$1"
  # 密码只在 MySQL 容器内通过环境变量展开，避免出现在宿主命令行或日志中。
  kubectl_ocm exec statefulset/mysql -- sh -c \
    'exec mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" --databases ocm --single-transaction --routines --events --set-gtid-purged=OFF' \
    >"$output"
  [[ -s "$output" ]] || fail "数据库备份为空: $output"
}

mysql_restore() {
  local input="$1"
  [[ -s "$input" ]] || fail "不存在可恢复的数据库备份: $input"
  # 先停 manager-api，避免旧/新应用在导入期间继续写库或启动自动迁移。
  kubectl_ocm scale deploy/manager-api --replicas=0
  kubectl_ocm rollout status deploy/manager-api --timeout="$ROLLOUT_TIMEOUT"
  # 基线备份包含完整表定义，必须先移除新版 schema；否则新增的外键和索引会使旧表定义导入失败。
  kubectl_ocm exec statefulset/mysql -- sh -c \
    'exec mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -e "DROP DATABASE IF EXISTS ocm; CREATE DATABASE ocm"'
  kubectl_ocm exec -i statefulset/mysql -- sh -c \
    'exec mysql -uroot -p"$MYSQL_ROOT_PASSWORD"' <"$input"
}

mysql_query() {
  local sql="$1"
  kubectl_ocm exec statefulset/mysql -- sh -c \
    "exec mysql -uroot -p\"\$MYSQL_ROOT_PASSWORD\" -D ocm -N -e $(printf '%q' "$sql")"
}

record_database_state() {
  local phase="$1" aicc_table_count
  local version
  version="$(mysql_query 'SELECT GROUP_CONCAT(version ORDER BY version) FROM schema_migrations;')"
  printf '%s migration_version=%s\n' "$phase" "${version:-<empty>}"
  mysql_query "SELECT 'organizations', COUNT(*) FROM organizations UNION ALL SELECT 'users', COUNT(*) FROM users UNION ALL SELECT 'apps', COUNT(*) FROM apps UNION ALL SELECT 'ragflow_datasets', COUNT(*) FROM ragflow_datasets UNION ALL SELECT 'ragflow_documents', COUNT(*) FROM ragflow_documents UNION ALL SELECT 'audit_logs', COUNT(*) FROM audit_logs;" \
    | sed "s/^/$phase count /"
  # AICC 表只在升级后存在；先检查 schema，再记录精确行数，保证 master 基线可重复执行。
  aicc_table_count="$(mysql_query "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='ocm' AND table_name='aicc_agents';")"
  if [[ "$aicc_table_count" == "1" ]]; then
    mysql_query "SELECT 'aicc_agents', COUNT(*) FROM aicc_agents UNION ALL SELECT 'aicc_sessions', COUNT(*) FROM aicc_sessions UNION ALL SELECT 'aicc_messages', COUNT(*) FROM aicc_messages UNION ALL SELECT 'aicc_leads', COUNT(*) FROM aicc_leads;" \
      | sed "s/^/$phase count /"
  fi
}

verify_baseline_counts() {
  local organizations apps
  organizations="$(mysql_query 'SELECT COUNT(*) FROM organizations;')"
  apps="$(mysql_query 'SELECT COUNT(*) FROM apps;')"
  [[ "$organizations" -ge 1 ]] || fail "master 基线企业未创建"
  [[ "$apps" -ge 1 ]] || fail "master 基线实例未创建"
}

image_tag() {
  local sha="$1"
  printf 'aicc-readiness-%s' "${sha:0:12}"
}

archive_source() {
  local sha="$1" destination="$2"
  mkdir -p "$destination"
  git -C "$REPO_ROOT" archive "$sha" | tar -x -C "$destination"
}

build_images() {
  local label="$1" sha="$2" source_dir="$3" tag
  tag="$(image_tag "$sha")"
  local api_image="$REGISTRY_HOST/oc-manager-api:$tag"
  local web_image="$REGISTRY_HOST/oc-manager-web:$tag"
  log "构建 $label 镜像 (SHA=$sha, tag=$tag)"
  # 历史 Web Dockerfile 未禁用 onnxruntime-node 的 CUDA 下载，安装阶段会直连 GitHub。
  # 管理端前端不使用 CUDA，临时归档源码统一跳过该可选依赖，确保升级演练可在国内网络完成。
  sed -i '/^WORKDIR \/src$/a ENV ONNXRUNTIME_NODE_INSTALL_CUDA=skip' "$source_dir/web/Dockerfile"
  docker build -t "$api_image" -f "$source_dir/cmd/server/Dockerfile" "$source_dir"
  docker build -t "$web_image" -f "$source_dir/web/Dockerfile" "$source_dir/web"
  docker push "$api_image"
  docker push "$web_image"
  if [[ "$label" == "master" ]]; then
    MASTER_API_IMAGE="$api_image"
    MASTER_WEB_IMAGE="$web_image"
  else
    KEFU_API_IMAGE="$api_image"
    KEFU_WEB_IMAGE="$web_image"
  fi
}

reset_local_cluster() {
  log "重建本地 k3d 数据（仅本机 $CLUSTER_NAME）"
  k3d cluster delete "$CLUSTER_NAME" || true
  # hostPath 数据由集群内 root 写入，必须经一次性容器清理，不能假设宿主用户有权限。
  docker run --rm -v "$REPO_ROOT/.k3d-data:/data" \
    ywjs-26257ea5.ecis.huabei-3.cmecloud.cn/public/alpine:3.22 \
    sh -c 'rm -rf /data/* /data/.[!.]* 2>/dev/null' || true
  rm -rf "$REPO_ROOT/.k3d-data"
  make -C "$REPO_ROOT" cluster-create
  preload_cluster_images
  # k3d 的内置 Helm job 会异步创建 Traefik；必须等 Ingress controller 真正 Ready，
  # 后续 newapi.localhost/RAGFlow 初始化才不会把入口未启动误判为业务 API 超时。
  kubectl --context "$KUBE_CONTEXT" -n kube-system wait --for=create deployment/traefik --timeout="$ROLLOUT_TIMEOUT"
  kubectl --context "$KUBE_CONTEXT" -n kube-system rollout status deploy/traefik --timeout="$ROLLOUT_TIMEOUT"
}

preload_cluster_images() {
  local image
  # k3d image import 在本机对 OCI manifest 偶发报错却返回成功；直接交给 server 节点 ctr，
  # 让升级演练在 MySQL/RAGFlow 等基础镜像上得到可验证的预加载结果。
  # k3s 安装 Traefik 依赖 controller 与 Helm job 两个镜像，均从 DaoCloud 固定源拉取，
  # 再改回内置 manifest 期望的原始引用，避免节点回退直连 Docker Hub。
  docker image inspect "$TRAEFIK_IMAGE" >/dev/null 2>&1 || {
    docker pull "$TRAEFIK_MIRROR_IMAGE"
    docker tag "$TRAEFIK_MIRROR_IMAGE" "$TRAEFIK_IMAGE"
  }
  docker image inspect "$KLIPPER_HELM_IMAGE" >/dev/null 2>&1 || {
    docker image inspect "$KLIPPER_HELM_MIRROR_IMAGE" >/dev/null 2>&1 || docker pull "$KLIPPER_HELM_MIRROR_IMAGE"
    docker tag "$KLIPPER_HELM_MIRROR_IMAGE" "$KLIPPER_HELM_IMAGE"
  }
  for image in "$TRAEFIK_IMAGE" "$KLIPPER_HELM_IMAGE" busybox:1.36 mysql:8.0 elasticsearch:8.11.3 pgsty/minio:RELEASE.2026-03-25T00-00-00Z calciumion/new-api:latest infiniflow/ragflow:v0.25.6; do
    docker image inspect "$image" >/dev/null 2>&1 || docker pull "$image"
    # Docker 本机保存的多架构 OCI 索引会让 containerd 导入缺失非当前平台 digest；只导出节点的 amd64 manifest。
    docker save --platform linux/amd64 "$image" | docker exec -i "k3d-${CLUSTER_NAME}-server-0" ctr images import -
  done
}

apply_stack_with_images() {
  local api_image="$1" web_image="$2"
  local local_dir="$REPO_ROOT/deploy/k8s/local"
  log "部署镜像 api=$api_image web=$web_image"
  # cluster reset 会删除本地 k3d registry；临时升级镜像必须在新 registry 就绪后重新推送。
  docker push "$api_image"
  docker push "$web_image"
  kubectl --context "$KUBE_CONTEXT" apply -f "$local_dir/00-namespace.yaml"
  kubectl_ocm apply -f "$local_dir/secret.yaml"
  kubectl_ocm apply -f "$local_dir/mysql.yaml" -f "$local_dir/redis.yaml" -f "$local_dir/elasticsearch.yaml" -f "$local_dir/minio.yaml"
  kubectl_ocm rollout status statefulset/mysql --timeout="$ROLLOUT_TIMEOUT"
  kubectl_ocm rollout status statefulset/redis --timeout="$ROLLOUT_TIMEOUT"
  kubectl_ocm rollout status statefulset/minio --timeout="$ROLLOUT_TIMEOUT"
  kubectl_ocm rollout status statefulset/elasticsearch --timeout="$ROLLOUT_TIMEOUT"
  kubectl_ocm exec statefulset/minio -- sh -c 'mc alias set local http://127.0.0.1:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" >/dev/null 2>&1; mc mb -p local/oc-apps; mc mb -p local/ragflow'
  # manager-rbac 同时声明 ocm 与 oc-apps 资源，不能通过 kubectl_ocm 强制覆盖 namespace。
  kubectl --context "$KUBE_CONTEXT" apply -f "$local_dir/manager-rbac.yaml"
  kubectl_ocm apply -f "$local_dir/new-api.yaml" -f "$local_dir/ragflow.yaml" -f "$local_dir/ingress.yaml"
  # 以流方式替换 image，避免 manifest 中的 :dev 镜像在首次调度时抢先运行 kefu migration。
  sed "s#k3d-ocm-registry.localhost:5000/oc-manager-api:dev#$api_image#" "$local_dir/manager-api.yaml" | kubectl --context "$KUBE_CONTEXT" apply -f -
  sed "s#k3d-ocm-registry.localhost:5000/oc-manager-web:dev#$web_image#" "$local_dir/manager-web.yaml" | kubectl --context "$KUBE_CONTEXT" apply -f -
  kubectl_ocm rollout status deploy/new-api --timeout="$ROLLOUT_TIMEOUT"
  kubectl_ocm rollout status deploy/ragflow --timeout="$ROLLOUT_TIMEOUT"
  kubectl_ocm rollout status deploy/manager-api --timeout="$ROLLOUT_TIMEOUT"
  kubectl_ocm rollout status deploy/manager-web --timeout="$ROLLOUT_TIMEOUT"
}

initialize_model_services_without_mutating_repo() {
  local temp_initializer="$TEMP_DIR/local-init-models.py" escaped_repo escaped_secret
  # 初始化器会回填 secret；副本指向临时 secret，保证本次演练不修改或提交工作区文件。
  cp "$REPO_ROOT/scripts/local-init-models.py" "$temp_initializer"
  escaped_repo="${REPO_ROOT//\\/\\\\}"
  escaped_repo="${escaped_repo//\"/\\\"}"
  escaped_secret="${TEMP_DIR}/secret.yaml"
  escaped_secret="${escaped_secret//\\/\\\\}"
  escaped_secret="${escaped_secret//\"/\\\"}"
  sed -i "s|^ROOT = .*|ROOT = \"$escaped_repo\"|; s|^SECRET_FILE = .*|SECRET_FILE = \"$escaped_secret\"|" "$temp_initializer"
  cp "$REPO_ROOT/deploy/k8s/local/secret.yaml" "${TEMP_DIR}/secret.yaml"
  log "初始化本地 new-api 与 RAGFlow（临时 Secret）"
  python3 "$temp_initializer"
}

seed_master_history() {
  log "创建 master 基线企业、管理员、实例、知识库与可识别历史记录"
  kubectl_ocm exec deploy/manager-api -- env OCM_E2E=1 seed-e2e >/dev/null
  # seed-e2e 创建企业、组织管理员和运行实例；补一条组织知识库映射与审计记录作为可识别历史数据。
  mysql_query "INSERT INTO ragflow_datasets (id, scope_type, org_id, app_id, name, status) SELECT UUID(), 'org', id, NULL, 'aicc-readiness-master-knowledge', 'active' FROM organizations LIMIT 1;"
  mysql_query "INSERT INTO audit_logs (id, actor_role, target_type, target_id, action, result, detail_message) VALUES (UUID(), 'system', 'aicc-readiness', 'master-baseline', 'upgrade-drill-baseline', 'succeeded', 'master baseline history');"
  verify_baseline_counts
}

switch_application_images() {
  local api_image="$1" web_image="$2" phase="$3"
  local api_status=0 web_status=0
  log "$phase 切换应用镜像"
  kubectl_ocm set image deploy/manager-api manager-api="$api_image"
  kubectl_ocm set image deploy/manager-web manager-web="$web_image"
  # 两个组件都要等待并记录结果，避免后一条成功命令掩盖前一个组件的滚动失败。
  if ! kubectl_ocm rollout status deploy/manager-api --timeout="$ROLLOUT_TIMEOUT"; then
    api_status=1
  fi
  if ! kubectl_ocm rollout status deploy/manager-web --timeout="$ROLLOUT_TIMEOUT"; then
    web_status=1
  fi
  kubectl_ocm get deploy manager-api manager-web -o jsonpath='{range .items[*]}{.metadata.name}{" "}{range .spec.template.spec.containers[*]}{.image}{"\n"}{end}{end}'
  if ((api_status != 0 || web_status != 0)); then
    return 1
  fi
}

browser_smoke() {
  local phase="$1"
  if [[ "$RUN_BROWSER_SMOKE" != "1" ]]; then
    printf '%s browser_smoke=SKIPPED (AICC_READINESS_RUN_BROWSER_SMOKE=%s)\n' "$phase" "$RUN_BROWSER_SMOKE"
    return
  fi
  log "$phase 真实浏览器核心冒烟"
  # 使用既有 Playwright 场景覆盖平台入口、企业入口、公开消息、知识范围、会话和线索闭环。
  # 临时验证副本不保留 node_modules；跳过 onnxruntime 的 CUDA 可选下载，避免安装时直连 GitHub。
  ONNXRUNTIME_NODE_INSTALL_CUDA=skip npm --prefix "$REPO_ROOT/web" ci --no-audit --no-fund
  npm --prefix "$REPO_ROOT/web" run test:e2e -- aicc.spec.ts
}

restore_kefu_on_exit() {
  local status=$?
  if [[ -n "$TEMP_DIR" ]]; then
    rm -rf "$TEMP_DIR"
  fi
  if [[ "$FINAL_KEFU_DEPLOYED" != "1" && -n "$KEFU_API_IMAGE" && -n "$KEFU_WEB_IMAGE" ]]; then
    printf '\n==> trap: 尝试恢复 KEFU_SHA=%s 镜像\n' "$KEFU_SHA" >&2
    kubectl_ocm set image deploy/manager-api manager-api="$KEFU_API_IMAGE" >/dev/null 2>&1 || true
    kubectl_ocm set image deploy/manager-web manager-web="$KEFU_WEB_IMAGE" >/dev/null 2>&1 || true
    kubectl_ocm rollout status deploy/manager-api --timeout="$ROLLOUT_TIMEOUT" >/dev/null 2>&1 || true
    kubectl_ocm rollout status deploy/manager-web --timeout="$ROLLOUT_TIMEOUT" >/dev/null 2>&1 || true
  fi
  exit "$status"
}

main() {
  require_command git
  require_command docker
  require_command k3d
  require_command kubectl
  require_command npm
  require_local_environment
  MASTER_SHA="$(git -C "$REPO_ROOT" rev-parse master)"
  KEFU_SHA="$(git -C "$REPO_ROOT" rev-parse HEAD)"
  printf 'MASTER_SHA=%s\nKEFU_SHA=%s\n' "$MASTER_SHA" "$KEFU_SHA"
  [[ "$MASTER_SHA" != "$KEFU_SHA" ]] || fail "master 与 kefu SHA 相同，无法执行升级演练"
  TEMP_DIR="$(mktemp -d /tmp/aicc-readiness-upgrade.XXXXXX)"
  trap restore_kefu_on_exit EXIT

  if kubectl_ocm get statefulset/mysql >/dev/null 2>&1; then
    log "备份当前本地数据库到 $CURRENT_BACKUP"
    mysql_dump "$CURRENT_BACKUP"
  else
    log "当前集群没有 MySQL，跳过中断前环境备份"
  fi
  archive_source "$MASTER_SHA" "$TEMP_DIR/master"
  archive_source "$KEFU_SHA" "$TEMP_DIR/kefu"
  build_images master "$MASTER_SHA" "$TEMP_DIR/master"
  build_images kefu "$KEFU_SHA" "$TEMP_DIR/kefu"

  reset_local_cluster
  # registry 随 k3d 一同重建；回滚阶段、运行时实例和 EXIT trap 都依赖这些镜像已重新推送。
  docker push "$KEFU_API_IMAGE"
  docker push "$KEFU_WEB_IMAGE"
  docker image inspect "$LOCAL_OPS_IMAGE" >/dev/null 2>&1 || fail "缺少本地运行时镜像: $LOCAL_OPS_IMAGE"
  docker image inspect "$LOCAL_HERMES_IMAGE" >/dev/null 2>&1 || fail "缺少本地 Hermes 镜像: $LOCAL_HERMES_IMAGE"
  docker push "$LOCAL_OPS_IMAGE"
  docker push "$LOCAL_HERMES_IMAGE"
  apply_stack_with_images "$MASTER_API_IMAGE" "$MASTER_WEB_IMAGE"
  initialize_model_services_without_mutating_repo
  seed_master_history
  record_database_state master-baseline
  log "备份 master 基线数据库到 $BASELINE_BACKUP"
  mysql_dump "$BASELINE_BACKUP"

  switch_application_images "$KEFU_API_IMAGE" "$KEFU_WEB_IMAGE" upgrade-to-kefu
  record_database_state kefu-upgrade
  verify_baseline_counts
  browser_smoke kefu-upgrade
  printf 'PASS upgrade MASTER_SHA=%s -> KEFU_SHA=%s\n' "$MASTER_SHA" "$KEFU_SHA"

  # 新 schema 下旧应用能否启动由实际 healthz/rollout 决定；若失败，明确记录边界并立即恢复新版。
  if switch_application_images "$MASTER_API_IMAGE" "$MASTER_WEB_IMAGE" rollback-to-master; then
    kubectl_ocm exec deploy/manager-api -- wget -qO- http://127.0.0.1:8080/healthz >/dev/null
    record_database_state master-rollback
    printf 'PASS rollback-boundary master application started on upgraded schema\n'
  else
    printf 'PASS rollback-boundary controlled failure: master cannot run on upgraded schema; restoring baseline backup=%s\n' "$BASELINE_BACKUP"
    mysql_restore "$BASELINE_BACKUP"
    kubectl_ocm scale deploy/manager-api --replicas=1
    kubectl_ocm rollout status deploy/manager-api --timeout="$ROLLOUT_TIMEOUT"
    record_database_state master-restored-from-backup
    verify_baseline_counts
    printf 'PASS rollback-backup-restore master baseline database restored\n'
  fi

  switch_application_images "$KEFU_API_IMAGE" "$KEFU_WEB_IMAGE" restore-kefu
  record_database_state kefu-restored
  browser_smoke kefu-restored
  FINAL_KEFU_DEPLOYED=1
  printf 'PASS final-environment KEFU_SHA=%s api=%s web=%s backup=%s\n' "$KEFU_SHA" "$KEFU_API_IMAGE" "$KEFU_WEB_IMAGE" "$BASELINE_BACKUP"
}

main "$@"
