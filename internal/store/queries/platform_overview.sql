-- name: CountActiveOrganizations :one
-- 平台总览组织计数：剔除 soft-deleted；status='active' 与 'disabled' 都算入册组织。
SELECT COUNT(*) AS count FROM organizations WHERE deleted_at IS NULL;

-- name: CountActiveUsers :one
-- 平台总览成员计数：仅 active 状态、非 platform_admin。
-- users 表当前没有 soft-delete 字段，status='disabled' 视为下线。
SELECT COUNT(*) AS count FROM users
WHERE status = 'active' AND role != 'platform_admin';

-- name: CountAppsByStatus :many
-- 平台总览应用计数：按 status 分组，soft-deleted 通过 deleted_at IS NULL 排除。
-- 调用方在 service 层把结果聚合成 {status: count}，未出现 status 视为 0。
SELECT status, COUNT(*) AS count FROM apps
WHERE deleted_at IS NULL
GROUP BY status;
