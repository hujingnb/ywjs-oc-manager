-- name: CreateConversationFile :exec
INSERT INTO conversation_files (
    id, app_id, session_id, s3_key, filename, mime, size
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetConversationFile :one
SELECT * FROM conversation_files WHERE id = ?;
