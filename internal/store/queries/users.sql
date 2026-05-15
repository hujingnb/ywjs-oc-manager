-- name: CreateUser :one
INSERT INTO users (
    org_id,
    username,
    password_hash,
    display_name,
    role,
    status
) VALUES (
    $1, $2, $3, $4, $5, $6
)
RETURNING *;

-- name: GetUser :one
SELECT *
FROM users
WHERE id = $1;

-- name: GetUserByUsername :one
SELECT *
FROM users
WHERE org_id IS NULL AND username = $1;

-- name: GetUserByOrgAndUsername :one
SELECT *
FROM users
WHERE org_id = $1 AND username = $2;

-- name: ListUsersByOrg :many
SELECT *
FROM users
WHERE org_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2 OFFSET $3;

-- name: GetOrgAdminByOrg :one
-- 组织列表复制登录信息时只需要一个可登录的组织管理员用户名。
-- 密码明文不落库，因此这里只返回账号名，密码提示由调用方生成。
SELECT *
FROM users
WHERE org_id = $1
  AND role = 'org_admin'
  AND deleted_at IS NULL
ORDER BY created_at ASC, id ASC
LIMIT 1;

-- name: SetUserStatus :one
-- disabled 时同步写 deleted_at（下线时间戳）；enabled 时清空，让重启用户能恢复。
UPDATE users
SET status = $2,
    deleted_at = CASE WHEN $2 = 'disabled' THEN NOW() ELSE NULL END,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateUserProfile :one
UPDATE users
SET display_name = $2, role = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateUserPassword :one
UPDATE users
SET password_hash = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SoftDeleteUser :exec
-- 真软删除：仅设置 deleted_at（不动 status）；status 与 deleted_at 语义独立。
UPDATE users SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;

-- name: MarkUserLoggedIn :one
UPDATE users
SET last_login_at = now(), updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ListUsersByOrgWithActiveApp :many
-- 列出组织内成员及其当前关联的活跃实例（LEFT JOIN，无实例的成员仍返回）。
-- apps 表上 apps_owner_active 唯一约束保证每个 owner 最多一个未软删实例，
-- LEFT JOIN 不会产生重复行；ORDER BY 保持与 ListUsersByOrg 一致。
SELECT u.*, a.id AS active_app_id, a.name AS active_app_name
FROM users u
LEFT JOIN apps a
  ON a.owner_user_id = u.id AND a.deleted_at IS NULL
WHERE u.org_id = $1
ORDER BY u.created_at DESC, u.id DESC
LIMIT $2 OFFSET $3;
