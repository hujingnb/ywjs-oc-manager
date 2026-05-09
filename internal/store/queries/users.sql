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
WHERE username = $1;

-- name: ListUsersByOrg :many
SELECT *
FROM users
WHERE org_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2 OFFSET $3;

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
