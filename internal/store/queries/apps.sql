-- name: CreateApp :exec
INSERT INTO apps (
    id,
    org_id,
    owner_user_id,
    runtime_node_id,
    name,
    description,
    status,
    api_key_status,
    version_id
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?
);

-- name: GetApp :one
SELECT *
FROM apps
WHERE id = ?;

-- name: GetActiveAppByOwner :one
SELECT *
FROM apps
WHERE owner_user_id = ? AND deleted_at IS NULL;

-- name: ListAppsByOrg :many
SELECT *
FROM apps
WHERE org_id = ? AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: ListAppsByRuntimeNode :many
SELECT *
FROM apps
WHERE runtime_node_id = ? AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: SetAppStatus :exec
UPDATE apps
SET status = ?, updated_at = now()
WHERE id = ?;

-- name: SetAppContainer :exec
UPDATE apps
SET container_id = ?, container_name = ?, updated_at = now()
WHERE id = ?;

-- name: SetAppNewAPIKey :exec
UPDATE apps
SET
    newapi_key_id = ?,
    newapi_key_ciphertext = ?,
    api_key_status = ?,
    newapi_key_name = ?,
    updated_at = now()
WHERE id = ?;

-- name: SetAppRuntimeToken :exec
-- 首次写入 per-app control token（三用：bootstrap / oc-kb / oc-ops）；并发重复初始化拿不到行，由 service 读取既有 token。
UPDATE apps
SET runtime_token_hash = ?,
    runtime_token_ciphertext = ?,
    updated_at = now()
WHERE id = ?
  AND deleted_at IS NULL
  AND runtime_token_hash IS NULL
  AND runtime_token_ciphertext IS NULL;

-- name: GetAppByRuntimeTokenHash :one
-- 按 control token（per-app 三用：bootstrap / oc-kb / oc-ops）的 hash 反查当前 app；
-- 不允许请求方传入目标 app/dataset，鉴权即定位。
SELECT *
FROM apps
WHERE runtime_token_hash = ? AND deleted_at IS NULL;

-- name: SoftDeleteApp :exec
UPDATE apps
SET status = 'deleted', deleted_at = now(), updated_at = now()
WHERE id = ? AND deleted_at IS NULL;

-- name: ListRunningApps :many
-- 列出当前期望持有 runtime 容器的应用，供 scheduler 周期 dispatch
-- runtime_refresh_status 与 app_health_check job。
-- running 是常态；binding_waiting 表示容器已起但渠道还在登录中，依然要刷指标。
SELECT id, runtime_node_id, container_id
FROM apps
WHERE deleted_at IS NULL
  AND status IN ('running', 'binding_waiting')
  AND runtime_node_id IS NOT NULL
  AND container_id IS NOT NULL
ORDER BY id;

-- name: SetAppRuntimeSnapshot :exec
UPDATE apps
SET runtime_snapshot_json = ?,
    runtime_snapshot_at = now(),
    updated_at = now()
WHERE id = ?;

-- name: SetAppRestartPolicy :exec
-- 管理员 PATCH /apps/:appId/restart-policy 写入；mode/max_per_window/window_seconds 校验在 service 层。
UPDATE apps
SET restart_policy_json = ?,
    updated_at = now()
WHERE id = ?;

-- name: SetAppHealthState :exec
-- worker app_health_check handler 写最近一次健康检查结果；用于自动重启窗口计数。
UPDATE apps
SET health_state_json = ?,
    updated_at = now()
WHERE id = ?;

-- name: SetAppProgress :exec
-- progressReporter 节流后写入；NULL/NULL 表示阶段切换或未知。
UPDATE apps
SET progress_current = ?,
    progress_total = ?,
    updated_at = now()
WHERE id = ?;

-- name: ClearAppProgress :exec
-- transitionTo / RequestInitialize 强制清空进度字段。
UPDATE apps
SET progress_current = NULL,
    progress_total = NULL,
    updated_at = now()
WHERE id = ?;

-- name: MarkAppFailed :exec
-- 任意状态 → error 时同时写入来源状态与错误文本，保留"在哪一步失败"与"为什么失败"语义。
-- last_error_status 不加 CHECK 约束，值由调用方在 Go 层负责合法性。
UPDATE apps
SET status = 'error',
    last_error_status = ?,
    last_error_message = ?,
    progress_current = NULL,
    progress_total = NULL,
    updated_at = now()
WHERE id = ?;

-- name: ListStaleInits :many
-- reaper 扫描 init 子状态下连续 90s 无更新的孤儿；阈值由调用方传入。
-- 包含新旧两套 init 状态，确保历史孤儿也能被正确清理。
SELECT id, runtime_node_id, status
FROM apps
WHERE deleted_at IS NULL
  AND status IN ('pulling_runtime_image','preparing_runtime','creating_container','starting')
  AND updated_at < ?
ORDER BY id;

-- name: UpdateAppRuntimeImage :exec
-- phasePullRuntimeImage 成功后写入镜像引用与 sha256。
UPDATE apps
SET
    runtime_image_ref    = ?,
    runtime_image_sha256 = ?,
    updated_at = now()
WHERE id = ?;

-- name: SetAppAppliedVersion :exec
-- 初始化/重启成功后记录已应用的版本修订与镜像 ref，用于 version_synced 检测。
UPDATE apps
SET applied_version_revision = ?,
    applied_image_ref = ?,
    updated_at = now()
WHERE id = ?;

-- name: SetAppVersion :exec
-- 切换实例绑定的助手版本，并把 applied_version_revision / applied_image_ref 清零。
-- 不同版本各自维护独立的 revision 计数，若切换后保留旧 applied_*，当新旧版本
-- 的 revision 数字恰好相同（且镜像相同）时 version_synced 会误判为已同步。
-- 清零后 applied_version_revision=0 永远不等于任何真实版本 revision（从 1 起），
-- 实例切换后必然进入需重启态，直到重启重新写入 applied_*。
UPDATE apps
SET version_id = ?,
    applied_version_revision = 0,
    applied_image_ref = '',
    updated_at = now()
WHERE id = ? AND deleted_at IS NULL;

-- name: GetAppWithVersion :one
-- 取实例及其绑定版本的 revision / image_id，供 version_synced 计算。
SELECT sqlc.embed(apps), av.revision AS version_revision, av.image_id AS version_image_id
FROM apps
JOIN assistant_versions av ON av.id = apps.version_id
WHERE apps.id = ?;

-- name: ListAppsByOrgWithVersion :many
-- 组织实例列表联查版本 revision / image_id，供 version_synced 批量计算。
SELECT sqlc.embed(apps), av.revision AS version_revision, av.image_id AS version_image_id
FROM apps
JOIN assistant_versions av ON av.id = apps.version_id
WHERE apps.org_id = ? AND apps.deleted_at IS NULL
ORDER BY apps.created_at DESC, apps.id DESC
LIMIT ? OFFSET ?;
