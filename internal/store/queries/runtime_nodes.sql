-- name: CreateRuntimeNode :one
INSERT INTO runtime_nodes (
    name,
    status,
    agent_docker_endpoint,
    agent_file_endpoint,
    agent_tls_ca_cert,
    bootstrap_token_hash,
    bootstrap_token_expires_at,
    heartbeat_interval_seconds,
    node_data_root
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
)
RETURNING *;

-- name: GetRuntimeNode :one
SELECT *
FROM runtime_nodes
WHERE id = $1;

-- name: GetRuntimeNodeByName :one
SELECT *
FROM runtime_nodes
WHERE name = $1;

-- name: ListRuntimeNodes :many
SELECT *
FROM runtime_nodes
ORDER BY created_at DESC, id DESC
LIMIT $1 OFFSET $2;

-- name: RegisterRuntimeNode :one
UPDATE runtime_nodes
SET
    status = 'active',
    agent_docker_endpoint = $2,
    agent_file_endpoint = $3,
    agent_tls_ca_cert = $4,
    agent_token_hash = $5,
    bootstrap_token_hash = NULL,
    bootstrap_token_expires_at = NULL,
    agent_version = $6,
    node_data_root = $7,
    registered_at = COALESCE(registered_at, now()),
    last_heartbeat_at = now(),
    resource_snapshot_json = $8,
    metadata_json = $9,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateRuntimeNodeHeartbeat :one
UPDATE runtime_nodes
SET
    status = 'active',
    agent_version = $2,
    last_heartbeat_at = now(),
    resource_snapshot_json = $3,
    metadata_json = $4,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetRuntimeNodeStatus :one
UPDATE runtime_nodes
SET status = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateRuntimeNodeMaxApps :one
UPDATE runtime_nodes
SET max_apps = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- ListActiveNodesWithAppCounts 列出所有 active 节点并附带其当前未删除应用数量。
-- OnboardingService 自动选节点时按剩余容量过滤；剩余容量 = max_apps - app_count，
-- max_apps NULL 表示不限。
-- name: ListActiveNodesWithAppCounts :many
SELECT n.*,
       COALESCE(c.app_count, 0)::bigint AS app_count
FROM runtime_nodes n
LEFT JOIN (
    SELECT runtime_node_id, COUNT(*) AS app_count
    FROM apps
    WHERE deleted_at IS NULL
    GROUP BY runtime_node_id
) c ON c.runtime_node_id = n.id
WHERE n.status = 'active'
ORDER BY n.name ASC;
