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
WHERE session_token = ? AND expires_at > now();

-- name: MarkAICCSessionConsented :execrows
UPDATE aicc_sessions
SET privacy_consented_at = now(), updated_at = now()
WHERE session_token = ? AND expires_at > now();

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

-- name: ListAICCLeadFieldsByAgent :many
SELECT *
FROM aicc_lead_fields
WHERE agent_id = ?
ORDER BY sort_order ASC, id ASC;

-- name: UpsertAICCLeadValue :exec
INSERT INTO aicc_lead_values (
    id, session_id, agent_id, org_id, field_id, value_text, value_hash
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    value_text = VALUES(value_text),
    value_hash = VALUES(value_hash);

-- name: ListRequiredAICCLeadFieldsMissing :many
SELECT f.*
FROM aicc_lead_fields f
JOIN aicc_sessions s ON s.agent_id = f.agent_id
LEFT JOIN aicc_lead_values v ON v.session_id = s.id AND v.field_id = f.id
WHERE s.id = ? AND f.required = TRUE AND v.id IS NULL
ORDER BY f.sort_order ASC, f.id ASC;

-- name: UpdateAICCSessionLeadStatus :exec
UPDATE aicc_sessions
SET lead_status = ?, updated_at = now()
WHERE id = ?;

-- name: GetAICCAssistantMessageForFeedback :one
SELECT m.*
FROM aicc_messages m
JOIN aicc_sessions s ON s.id = m.session_id
WHERE m.id = ? AND m.direction = 'assistant' AND s.expires_at > now();

-- name: UpsertAICCFeedback :exec
INSERT INTO aicc_feedback (id, session_id, message_id, helpful)
VALUES (?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    helpful = VALUES(helpful);

-- name: UpdateAICCSessionResolutionStatus :exec
UPDATE aicc_sessions
SET resolution_status = ?, updated_at = now()
WHERE id = ?;

-- name: ListExpiredAICCSessions :many
SELECT *
FROM aicc_sessions
WHERE expires_at < now()
ORDER BY expires_at ASC, id ASC
LIMIT ?;

-- name: DeleteAICCSession :exec
DELETE FROM aicc_sessions
WHERE id = ?;
