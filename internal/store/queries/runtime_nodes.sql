-- name: EnrollRuntimeNodeInsert :exec
INSERT INTO runtime_nodes (
    id,
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
    ?, ?, ?, 'active', ?, ?, ?, ?, ?, ?, ?, ?, now(), now(), ?, ?, ?
);

-- name: GetRuntimeNode :one
SELECT *
FROM runtime_nodes
WHERE id = ?;

-- name: GetRuntimeNodeByName :one
SELECT *
FROM runtime_nodes
WHERE name = ?;

-- name: GetRuntimeNodeByAgentID :one
SELECT *
FROM runtime_nodes
WHERE agent_id = ?;

-- name: ListRuntimeNodes :many
SELECT *
FROM runtime_nodes
ORDER BY created_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: EnrollRuntimeNodeUpdate :exec
UPDATE runtime_nodes
SET
    name = ?,
    status = 'active',
    max_apps = ?,
    agent_docker_endpoint = ?,
    agent_file_endpoint = ?,
    agent_tls_ca_cert = ?,
    agent_token_hash = ?,
    agent_version = ?,
    node_data_root = ?,
    last_heartbeat_at = now(),
    resource_snapshot_json = ?,
    metadata_json = ?,
    agent_token_ciphertext = ?,
    probe_failure_streak = 0,
    probe_success_streak = 0,
    updated_at = now()
WHERE agent_id = ?;

-- name: UpdateRuntimeNodeHeartbeat :exec
UPDATE runtime_nodes
SET
    status = CASE
        WHEN status = 'unreachable' THEN 'active'
        ELSE status
    END,
    agent_version = ?,
    last_heartbeat_at = now(),
    resource_snapshot_json = ?,
    metadata_json = ?,
    updated_at = now()
WHERE id = ?;

-- name: UpdateRuntimeNodeProbeSuccess :exec
UPDATE runtime_nodes
SET
    status = CASE
        WHEN status = 'degraded' AND probe_success_streak + 1 >= ? THEN 'active'
        ELSE status
    END,
    last_probe_attempted_at = now(),
    last_probe_ok_at = now(),
    last_probe_error = NULL,
    probe_success_streak = probe_success_streak + 1,
    probe_failure_streak = 0,
    updated_at = now()
WHERE id = ?;

-- name: UpdateRuntimeNodeProbeFailure :exec
UPDATE runtime_nodes
SET
    status = CASE
        WHEN status = 'active' AND probe_failure_streak + 1 >= ? THEN 'degraded'
        ELSE status
    END,
    last_probe_attempted_at = now(),
    last_probe_failed_at = now(),
    last_probe_error = ?,
    probe_failure_streak = probe_failure_streak + 1,
    probe_success_streak = 0,
    updated_at = now()
WHERE id = ?;

-- name: SetRuntimeNodeStatus :exec
UPDATE runtime_nodes
SET status = ?, updated_at = now()
WHERE id = ?;

-- ListActiveNodesWithAppCounts 列出所有 active 节点并附带其当前未删除应用数量。
-- OnboardingService 自动选节点时按剩余容量过滤；剩余容量 = max_apps - app_count，
-- max_apps NULL 表示不限。degraded / unreachable / disabled 均不参与新应用调度。
-- name: ListActiveNodesWithAppCounts :many
SELECT n.*,
       COALESCE(c.app_count, 0) AS app_count
FROM runtime_nodes n
LEFT JOIN (
    SELECT runtime_node_id, COUNT(*) AS app_count
    FROM apps
    WHERE deleted_at IS NULL
    GROUP BY runtime_node_id
) c ON c.runtime_node_id = n.id
WHERE n.status = 'active'
ORDER BY n.name ASC;
