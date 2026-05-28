-- name: CreateRefreshToken :exec
INSERT INTO refresh_tokens (
    id,
    user_id,
    token_hash,
    expires_at
) VALUES (
    ?, ?, ?, ?
);

-- name: GetRefreshTokenByHash :one
SELECT *
FROM refresh_tokens
WHERE token_hash = ?;

-- name: GetRefreshToken :one
SELECT *
FROM refresh_tokens
WHERE id = ?;

-- name: RevokeRefreshToken :exec
UPDATE refresh_tokens
SET revoked_at = now()
WHERE id = ? AND revoked_at IS NULL;

-- name: DeleteExpiredRefreshTokens :exec
DELETE FROM refresh_tokens
WHERE expires_at < now();
