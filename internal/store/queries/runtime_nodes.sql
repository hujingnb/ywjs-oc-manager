-- name: EnrollRuntimeNodeInsert :one
INSERT INTO runtime_nodes (
    agent_id,
    name,
    status,
    max_apps,
    agent_docker_endpoint,
    agent_file_endpoint,
    agent_tls_ca_cert,
    agent_token_hash,
    heartbeat_interval_seconds,
    agent_version,
    node_data_root,
    registered_at,
    last_heartbeat_at,
    resource_snapshot_json,
    metadata_json,
    agent_token_ciphertext
) VALUES (
    $1, $2, 'active', $3, $4, $5, $6, $7, $8, $9, $10, now(), now(), $11, $12, $13
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

-- name: GetRuntimeNodeByAgentID :one
SELECT *
FROM runtime_nodes
WHERE agent_id = $1;

-- name: ListRuntimeNodes :many
SELECT *
FROM runtime_nodes
ORDER BY created_at DESC, id DESC
LIMIT $1 OFFSET $2;

-- name: EnrollRuntimeNodeUpdate :one
UPDATE runtime_nodes
SET
    name = $2,
    status = 'active',
    max_apps = $3,
    agent_docker_endpoint = $4,
    agent_file_endpoint = $5,
    agent_tls_ca_cert = $6,
    agent_token_hash = $7,
    agent_version = $8,
    node_data_root = $9,
    last_heartbeat_at = now(),
    resource_snapshot_json = $10,
    metadata_json = $11,
    agent_token_ciphertext = $12,
    probe_failure_streak = 0,
    probe_success_streak = 0,
    updated_at = now()
WHERE agent_id = $1
RETURNING *;

-- name: UpdateRuntimeNodeHeartbeat :one
UPDATE runtime_nodes
SET
    status = CASE
        WHEN status = 'unreachable' THEN 'active'
        ELSE status
    END,
    agent_version = $2,
    last_heartbeat_at = now(),
    resource_snapshot_json = $3,
    metadata_json = $4,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateRuntimeNodeProbeSuccess :one
UPDATE runtime_nodes
SET
    status = CASE
        WHEN status = 'degraded' AND probe_success_streak + 1 >= $2 THEN 'active'
        ELSE status
    END,
    last_probe_attempted_at = now(),
    last_probe_ok_at = now(),
    last_probe_error = NULL,
    probe_success_streak = probe_success_streak + 1,
    probe_failure_streak = 0,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateRuntimeNodeProbeFailure :one
UPDATE runtime_nodes
SET
    status = CASE
        WHEN status = 'active' AND probe_failure_streak + 1 >= $2 THEN 'degraded'
        ELSE status
    END,
    last_probe_attempted_at = now(),
    last_probe_failed_at = now(),
    last_probe_error = $3,
    probe_failure_streak = probe_failure_streak + 1,
    probe_success_streak = 0,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetRuntimeNodeStatus :one
UPDATE runtime_nodes
SET status = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- ListActiveNodesWithAppCounts 列出所有 active 节点并附带其当前未删除应用数量。
-- OnboardingService 自动选节点时按剩余容量过滤；剩余容量 = max_apps - app_count，
-- max_apps NULL 表示不限。degraded / unreachable / disabled 均不参与新应用调度。
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
