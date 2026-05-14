-- name: CreateApp :one
INSERT INTO apps (
    org_id,
    owner_user_id,
    runtime_node_id,
    name,
    description,
    status,
    persona_mode,
    app_prompt,
    api_key_status,
    model_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
)
RETURNING *;

-- name: GetApp :one
SELECT *
FROM apps
WHERE id = $1;

-- name: GetActiveAppByOwner :one
SELECT *
FROM apps
WHERE owner_user_id = $1 AND deleted_at IS NULL;

-- name: ListAppsByOrg :many
SELECT *
FROM apps
WHERE org_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT $2 OFFSET $3;

-- name: ListAppsByRuntimeNode :many
SELECT *
FROM apps
WHERE runtime_node_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT $2 OFFSET $3;

-- name: SetAppStatus :one
UPDATE apps
SET status = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetAppContainer :one
UPDATE apps
SET container_id = $2, container_name = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetAppNewAPIKey :one
UPDATE apps
SET
    newapi_key_id = $2,
    newapi_key_ciphertext = $3,
    api_key_status = $4,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SoftDeleteApp :one
UPDATE apps
SET status = 'deleted', deleted_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

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

-- name: SetAppRuntimeSnapshot :one
UPDATE apps
SET runtime_snapshot_json = $2,
    runtime_snapshot_at = now(),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetAppRestartPolicy :one
-- 管理员 PATCH /apps/:appId/restart-policy 写入；mode/max_per_window/window_seconds 校验在 service 层。
UPDATE apps
SET restart_policy_json = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetAppHealthState :one
-- worker app_health_check handler 写最近一次健康检查结果；用于自动重启窗口计数。
UPDATE apps
SET health_state_json = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetAppModel :one
UPDATE apps
SET model_id = $2,
    updated_at = now()
WHERE id = $1
  AND deleted_at IS NULL
RETURNING *;
