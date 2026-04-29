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
UPDATE users
SET status = $2, updated_at = now()
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

-- name: MarkUserLoggedIn :one
UPDATE users
SET last_login_at = now(), updated_at = now()
WHERE id = $1
RETURNING *;
