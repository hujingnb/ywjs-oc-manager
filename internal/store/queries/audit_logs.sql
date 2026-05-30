-- name: CreateAuditLog :exec
INSERT INTO audit_logs (
    id,
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
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
);

-- name: GetAuditLog :one
SELECT *
FROM audit_logs
WHERE id = ?;

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
            WHERE al.target_type = 'app' AND a.id = al.target_id),
        (SELECT o.name FROM organizations o
            WHERE al.target_type = 'organization' AND o.id = al.target_id),
        (SELECT COALESCE(NULLIF(tu.display_name, ''), tu.username) FROM users tu
            WHERE al.target_type IN ('user', 'member') AND tu.id = al.target_id)
        -- runtime_node target_type 已随节点概念删除（spec-A2b），不再提供名称解析
    ) AS target_name,
    COALESCE(
        (SELECT a.deleted_at IS NOT NULL FROM apps a
            WHERE al.target_type = 'app' AND a.id = al.target_id),
        (SELECT o.deleted_at IS NOT NULL FROM organizations o
            WHERE al.target_type = 'organization' AND o.id = al.target_id),
        (SELECT tu.deleted_at IS NOT NULL FROM users tu
            WHERE al.target_type IN ('user', 'member') AND tu.id = al.target_id),
        false
    ) AS target_deleted
FROM audit_logs al
LEFT JOIN users au ON au.id = al.actor_id
WHERE al.org_id = ?
ORDER BY al.created_at DESC, al.id DESC
LIMIT ? OFFSET ?;

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
            WHERE al.target_type = 'app' AND a.id = al.target_id),
        (SELECT o.name FROM organizations o
            WHERE al.target_type = 'organization' AND o.id = al.target_id),
        (SELECT COALESCE(NULLIF(tu.display_name, ''), tu.username) FROM users tu
            WHERE al.target_type IN ('user', 'member') AND tu.id = al.target_id)
        -- runtime_node target_type 已随节点概念删除（spec-A2b），不再提供名称解析
    ) AS target_name,
    COALESCE(
        (SELECT a.deleted_at IS NOT NULL FROM apps a
            WHERE al.target_type = 'app' AND a.id = al.target_id),
        (SELECT o.deleted_at IS NOT NULL FROM organizations o
            WHERE al.target_type = 'organization' AND o.id = al.target_id),
        (SELECT tu.deleted_at IS NOT NULL FROM users tu
            WHERE al.target_type IN ('user', 'member') AND tu.id = al.target_id),
        false
    ) AS target_deleted
FROM audit_logs al
LEFT JOIN users au ON au.id = al.actor_id
WHERE al.target_type = ? AND al.target_id = ?
ORDER BY al.created_at DESC, al.id DESC
LIMIT ? OFFSET ?;
