-- name: CreateAICCAgent :exec
INSERT INTO aicc_agents (
    id, org_id, app_id, name, status, scenario, greeting, answer_boundary,
    privacy_mode, privacy_text, retention_days, theme_json, allowed_domains_json,
    public_token, widget_token
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetAICCAgent :one
SELECT *
FROM aicc_agents
WHERE id = ? AND deleted_at IS NULL;

-- name: GetAICCAgentByPublicToken :one
SELECT *
FROM aicc_agents
WHERE public_token = ? AND status = 'active' AND deleted_at IS NULL;

-- name: ListAICCAgentsByOrg :many
SELECT *
FROM aicc_agents
WHERE org_id = ? AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: CountAICCAgentsByOrg :one
SELECT COUNT(*)
FROM aicc_agents
WHERE org_id = ? AND deleted_at IS NULL;

-- name: UpdateAICCAgentProfile :exec
UPDATE aicc_agents
SET name = ?, scenario = ?, greeting = ?, answer_boundary = ?, privacy_mode = ?,
    privacy_text = ?, retention_days = ?, theme_json = ?, allowed_domains_json = ?,
    updated_at = now()
WHERE id = ? AND deleted_at IS NULL;

-- name: SetAICCAgentStatus :exec
UPDATE aicc_agents
SET status = ?, updated_at = now()
WHERE id = ? AND deleted_at IS NULL;

-- name: SoftDeleteAICCAgent :exec
UPDATE aicc_agents
SET status = 'deleted', deleted_at = now(), updated_at = now()
WHERE id = ? AND deleted_at IS NULL;

-- name: CreateAICCSession :exec
INSERT INTO aicc_sessions (
    id, agent_id, org_id, session_token, channel, source_url, referrer, region,
    ip_hash, user_agent_hash, privacy_notice_shown, expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetAICCSessionByToken :one
SELECT *
FROM aicc_sessions
WHERE session_token = ?;

-- name: MarkAICCSessionConsented :exec
UPDATE aicc_sessions
SET privacy_consented_at = now(), updated_at = now()
WHERE session_token = ?;

-- name: CreateAICCMessage :exec
INSERT INTO aicc_messages (
    id, session_id, agent_id, direction, content_type, text_content,
    image_object_key, image_mime, image_size_bytes, hermes_message_id,
    is_fallback, is_refusal, error_summary
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListAICCMessagesBySession :many
SELECT *
FROM aicc_messages
WHERE session_id = ?
ORDER BY created_at ASC, id ASC;

-- name: ListExpiredAICCSessions :many
SELECT *
FROM aicc_sessions
WHERE expires_at < now()
ORDER BY expires_at ASC, id ASC
LIMIT ?;

-- name: DeleteAICCSession :exec
DELETE FROM aicc_sessions
WHERE id = ?;
