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
    metadata_json,
    detail_message
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
)
RETURNING *;

-- name: ListAuditLogsByOrg :many
-- 返回审计行 + actor 实时名称 + target 实时名称（按 target_type 走子查询）。
-- 子查询里 WHERE al.target_type = X 保证 newapi_call 的 endpoint 字符串
-- 永不被尝试转 UUID，避开 cast error。
SELECT
    al.id,
    al.actor_id,
    al.actor_role,
    al.org_id,
    al.target_type,
    al.target_id,
    al.action,
    al.result,
    al.error_message,
    al.ip_address,
    al.metadata_json,
    al.created_at,
    al.detail_message,
    COALESCE(NULLIF(au.display_name, ''), au.username, '')          AS actor_name,
    COALESCE(au.deleted_at IS NOT NULL, false)                       AS actor_deleted,
    COALESCE(
        (SELECT a.name FROM apps a
            WHERE al.target_type = 'app' AND a.id::text = al.target_id),
        (SELECT o.name FROM organizations o
            WHERE al.target_type = 'organization' AND o.id::text = al.target_id),
        (SELECT COALESCE(NULLIF(tu.display_name, ''), tu.username) FROM users tu
            WHERE al.target_type IN ('user', 'member') AND tu.id::text = al.target_id),
        (SELECT n.name FROM runtime_nodes n
            WHERE al.target_type = 'runtime_node' AND n.id::text = al.target_id)
    ) AS target_name,
    COALESCE(
        (SELECT a.deleted_at IS NOT NULL FROM apps a
            WHERE al.target_type = 'app' AND a.id::text = al.target_id),
        (SELECT o.deleted_at IS NOT NULL FROM organizations o
            WHERE al.target_type = 'organization' AND o.id::text = al.target_id),
        (SELECT tu.deleted_at IS NOT NULL FROM users tu
            WHERE al.target_type IN ('user', 'member') AND tu.id::text = al.target_id),
        false
    ) AS target_deleted
FROM audit_logs al
LEFT JOIN users au ON au.id = al.actor_id
WHERE al.org_id = $1
ORDER BY al.created_at DESC, al.id DESC
LIMIT $2 OFFSET $3;

-- name: ListAuditLogsByTarget :many
-- 同 ListAuditLogsByOrg，按 target_type + target_id 过滤。
SELECT
    al.id,
    al.actor_id,
    al.actor_role,
    al.org_id,
    al.target_type,
    al.target_id,
    al.action,
    al.result,
    al.error_message,
    al.ip_address,
    al.metadata_json,
    al.created_at,
    al.detail_message,
    COALESCE(NULLIF(au.display_name, ''), au.username, '')          AS actor_name,
    COALESCE(au.deleted_at IS NOT NULL, false)                       AS actor_deleted,
    COALESCE(
        (SELECT a.name FROM apps a
            WHERE al.target_type = 'app' AND a.id::text = al.target_id),
        (SELECT o.name FROM organizations o
            WHERE al.target_type = 'organization' AND o.id::text = al.target_id),
        (SELECT COALESCE(NULLIF(tu.display_name, ''), tu.username) FROM users tu
            WHERE al.target_type IN ('user', 'member') AND tu.id::text = al.target_id),
        (SELECT n.name FROM runtime_nodes n
            WHERE al.target_type = 'runtime_node' AND n.id::text = al.target_id)
    ) AS target_name,
    COALESCE(
        (SELECT a.deleted_at IS NOT NULL FROM apps a
            WHERE al.target_type = 'app' AND a.id::text = al.target_id),
        (SELECT o.deleted_at IS NOT NULL FROM organizations o
            WHERE al.target_type = 'organization' AND o.id::text = al.target_id),
        (SELECT tu.deleted_at IS NOT NULL FROM users tu
            WHERE al.target_type IN ('user', 'member') AND tu.id::text = al.target_id),
        false
    ) AS target_deleted
FROM audit_logs al
LEFT JOIN users au ON au.id = al.actor_id
WHERE al.target_type = $1 AND al.target_id = $2
ORDER BY al.created_at DESC, al.id DESC
LIMIT $3 OFFSET $4;
