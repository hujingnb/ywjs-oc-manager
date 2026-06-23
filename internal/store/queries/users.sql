-- name: CreateUser :exec
INSERT INTO users (
    id,
    org_id,
    username,
    password_hash,
    display_name,
    role,
    status
) VALUES (
    ?, ?, ?, ?, ?, ?, ?
);

-- name: GetUser :one
SELECT *
FROM users
WHERE id = ?;

-- name: GetUserByUsername :one
SELECT *
FROM users
WHERE org_id IS NULL AND username = ?;

-- name: GetUserByOrgAndUsername :one
SELECT *
FROM users
WHERE org_id = ? AND username = ?;

-- name: ListUsersByOrg :many
SELECT *
FROM users
WHERE org_id = ?
ORDER BY created_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: GetOrgAdminByOrg :one
-- 组织列表复制登录信息时只需要一个可登录的组织管理员用户名。
-- 密码明文不落库，因此这里只返回账号名，密码提示由调用方生成。
SELECT *
FROM users
WHERE org_id = ?
  AND role = 'org_admin'
  AND deleted_at IS NULL
ORDER BY created_at ASC, id ASC
LIMIT 1;

-- name: SetUserStatus :exec
-- disabled 时同步写 deleted_at（下线时间戳）；enabled 时清空，让重启用户能恢复。
UPDATE users
SET status = sqlc.arg(status),
    deleted_at = CASE WHEN sqlc.arg(status) = 'disabled' THEN NOW() ELSE NULL END,
    updated_at = NOW()
WHERE id = sqlc.arg(id);

-- name: UpdateUserProfile :exec
UPDATE users
SET display_name = ?, role = ?, updated_at = now()
WHERE id = ?;

-- name: UpdateUserPassword :exec
UPDATE users
SET password_hash = ?, updated_at = now()
WHERE id = ?;

-- name: SoftDeleteUser :exec
-- 真软删除：仅设置 deleted_at（不动 status）；status 与 deleted_at 语义独立。
UPDATE users SET deleted_at = NOW(), updated_at = NOW()
WHERE id = ? AND deleted_at IS NULL;

-- name: MarkUserLoggedIn :exec
UPDATE users
SET last_login_at = now(), updated_at = now()
WHERE id = ?;

-- name: ListUsersByOrgWithActiveApp :many
-- 列出组织内成员及其当前关联的活跃实例（LEFT JOIN，无实例的成员仍返回）。
-- apps 表上 apps_owner_active 唯一约束保证每个 owner 最多一个未软删实例，
-- LEFT JOIN 不会产生重复行；ORDER BY 保持与 ListUsersByOrg 一致。
SELECT u.*, a.id AS active_app_id, a.name AS active_app_name
FROM users u
LEFT JOIN apps a
  ON a.owner_user_id = u.id AND a.deleted_at IS NULL
WHERE u.org_id = ?
ORDER BY u.created_at DESC, u.id DESC
LIMIT ? OFFSET ?;

-- name: UpdateUserLocale :exec
-- 更新用户界面语言偏好。locale 由 handler 校验取值集合后传入；NULL 表示重置为「未选择」。
UPDATE users
SET locale = sqlc.arg(locale), updated_at = now()
WHERE id = sqlc.arg(id);
