-- name: UpsertKnowledgeSyncStatus :one
-- 插入或更新 (org_id, node_id) 的最近同步状态。
-- last_success_at 通过 sqlc.arg 显式传入，调用方在 status='synced' 时传 now()，
-- 其它状态传 NULL 表示保留之前的成功时间（COALESCE 在 SQL 内处理）。
INSERT INTO knowledge_sync_status (
    org_id, node_id, status, last_success_at, last_error, updated_at
) VALUES (
    $1, $2, $3, $4, $5, now()
)
ON CONFLICT (org_id, node_id) DO UPDATE SET
    status          = EXCLUDED.status,
    last_success_at = COALESCE(EXCLUDED.last_success_at, knowledge_sync_status.last_success_at),
    last_error      = EXCLUDED.last_error,
    updated_at      = now()
RETURNING *;

-- name: ListKnowledgeSyncStatusByOrg :many
-- 列出某组织在所有节点上的最近同步状态。
SELECT *
FROM knowledge_sync_status
WHERE org_id = $1
ORDER BY updated_at DESC;

-- name: GetKnowledgeSyncStatus :one
-- 查询单个 (org, node) 对的状态，主要用于幂等判断。
SELECT *
FROM knowledge_sync_status
WHERE org_id = $1 AND node_id = $2;
