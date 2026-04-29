-- name: CreateAuditLog :one
INSERT INTO audit_logs (
    actor_id,
    actor_role,
    org_id,
    target_type,
    target_id,
    action,
    result,
    error_message,
    ip_address,
    metadata_json
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
)
RETURNING *;

-- name: ListAuditLogsByOrg :many
SELECT *
FROM audit_logs
WHERE org_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2 OFFSET $3;

-- name: ListAuditLogsByTarget :many
SELECT *
FROM audit_logs
WHERE target_type = $1 AND target_id = $2
ORDER BY created_at DESC, id DESC
LIMIT $3 OFFSET $4;
