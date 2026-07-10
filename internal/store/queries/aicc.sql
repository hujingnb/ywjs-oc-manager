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
WHERE (public_token = ? OR widget_token = ?) AND status = 'active' AND deleted_at IS NULL;

-- name: ListAICCAgentsByOrg :many
SELECT *
FROM aicc_agents
WHERE org_id = ? AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: ListAICCAgentKnowledge :many
SELECT *
FROM aicc_agent_knowledge
WHERE agent_id = ?
ORDER BY scope_type ASC, scope_identity_key ASC;

-- name: DeleteAICCAgentKnowledgeByAgent :exec
DELETE FROM aicc_agent_knowledge
WHERE agent_id = ?;

-- name: AddAICCAgentKnowledge :exec
INSERT INTO aicc_agent_knowledge (
    id, agent_id, agent_org_id, scope_type, org_id, app_id,
    industry_knowledge_base_id, ragflow_document_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

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

-- name: ListAICCSessionsByAgent :many
SELECT *
FROM aicc_sessions
WHERE agent_id = ?
  AND (sqlc.narg(resolution_status) IS NULL OR resolution_status = sqlc.narg(resolution_status))
  AND (sqlc.narg(lead_status) IS NULL OR lead_status = sqlc.narg(lead_status))
  AND (sqlc.narg(channel) IS NULL OR channel = sqlc.narg(channel))
  AND (
      sqlc.narg(keyword) IS NULL
      OR source_url LIKE CONCAT('%', sqlc.narg(keyword), '%')
      OR referrer LIKE CONCAT('%', sqlc.narg(keyword), '%')
  )
ORDER BY created_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: GetAICCSession :one
SELECT *
FROM aicc_sessions
WHERE id = ?;

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

-- name: CreateAICCImage :exec
INSERT INTO aicc_images (
    id, session_id, agent_id, org_id, object_key, mime, size_bytes, filename
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetAICCImageBySession :one
SELECT *
FROM aicc_images
WHERE id = ? AND session_id = ?;

-- name: ListAICCMessagesBySession :many
SELECT *
FROM aicc_messages
WHERE session_id = ?
ORDER BY created_at ASC, id ASC;

-- name: ListAICCLeadFieldsByAgent :many
SELECT *
FROM aicc_lead_fields
WHERE agent_id = ? AND deleted_at IS NULL
ORDER BY sort_order ASC, id ASC;

-- name: DeactivateAICCLeadFieldsByAgent :exec
UPDATE aicc_lead_fields
SET deleted_at = now(), updated_at = now()
WHERE agent_id = ? AND deleted_at IS NULL;

-- name: UpsertAICCLeadField :exec
INSERT INTO aicc_lead_fields (
    id, agent_id, field_key, label, field_type, required, prompt_text, sort_order
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    label = VALUES(label),
    field_type = VALUES(field_type),
    required = VALUES(required),
    prompt_text = VALUES(prompt_text),
    sort_order = VALUES(sort_order),
    deleted_at = NULL,
    updated_at = now();

-- name: UpsertAICCLeadValue :exec
INSERT INTO aicc_lead_values (
    id, session_id, agent_id, org_id, field_id, value_text, value_hash
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    value_text = VALUES(value_text),
    value_hash = VALUES(value_hash);

-- name: UpsertAICCLead :exec
INSERT INTO aicc_leads (
    id, org_id, primary_contact_hash, display_name, latest_session_id
) VALUES (?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    display_name = VALUES(display_name),
    latest_session_id = VALUES(latest_session_id),
    unread = TRUE,
    updated_at = now();

-- name: GetAICCLeadByContact :one
SELECT *
FROM aicc_leads
WHERE org_id = ? AND primary_contact_hash = ?;

-- name: AttachAICCLeadValuesToLead :exec
UPDATE aicc_lead_values
SET lead_id = ?, lead_org_id = ?
WHERE session_id = ? AND org_id = ?;

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
WHERE m.id = ? AND s.session_token = ? AND m.direction = 'assistant' AND s.expires_at > now();

-- name: UpsertAICCFeedback :exec
INSERT INTO aicc_feedback (id, session_id, message_id, helpful)
VALUES (?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    helpful = VALUES(helpful);

-- name: UpdateAICCSessionResolutionStatus :exec
UPDATE aicc_sessions
SET resolution_status = ?, updated_at = now()
WHERE id = ?;

-- name: ListAICCLeadsByOrg :many
SELECT *
FROM aicc_leads
WHERE org_id = ?
ORDER BY unread DESC, updated_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: ListAllAICCLeadsByOrg :many
SELECT *
FROM aicc_leads
WHERE org_id = ?
ORDER BY unread DESC, updated_at DESC, id DESC
LIMIT ?;

-- name: MarkAICCLeadRead :execrows
UPDATE aicc_leads
SET unread = FALSE, updated_at = now()
WHERE id = ? AND org_id = ?;

-- name: CountAICCTodaySessions :one
SELECT COUNT(*)
FROM aicc_sessions
WHERE org_id = ? AND created_at >= CURRENT_DATE();

-- name: CountAICCUnreadLeads :one
SELECT COUNT(*)
FROM aicc_leads
WHERE org_id = ? AND unread = TRUE;

-- name: CountAICCSessionsByResolution :one
SELECT COUNT(*)
FROM aicc_sessions
WHERE org_id = ? AND resolution_status = ?;

-- name: CountAICCCompletedLeadSessions :one
SELECT COUNT(*)
FROM aicc_sessions
WHERE org_id = ? AND lead_status = 'complete';

-- name: ListAICCTopVisitorQuestionsByOrg :many
SELECT TRIM(m.text_content) AS question,
       CAST(COUNT(*) AS SIGNED) AS count
FROM aicc_messages m
JOIN aicc_sessions s ON s.id = m.session_id
WHERE s.org_id = ?
  AND m.direction = 'visitor'
  AND m.text_content IS NOT NULL
  AND TRIM(m.text_content) <> ''
GROUP BY TRIM(m.text_content)
ORDER BY count DESC, question ASC
LIMIT ?;

-- name: ListAICCTopSourceURLsByOrg :many
SELECT source_url,
       CAST(COUNT(*) AS SIGNED) AS count
FROM aicc_sessions
WHERE org_id = ?
  AND source_url IS NOT NULL
  AND TRIM(source_url) <> ''
GROUP BY source_url
ORDER BY count DESC, source_url ASC
LIMIT ?;

-- name: ListExpiredAICCSessions :many
SELECT *
FROM aicc_sessions
WHERE expires_at < now()
ORDER BY expires_at ASC, id ASC
LIMIT ?;

-- name: ListAICCImageObjectKeysBySession :many
SELECT object_key
FROM aicc_images
WHERE session_id = ?
ORDER BY created_at ASC, id ASC;

-- name: ClearAICCLeadLatestSession :exec
UPDATE aicc_leads
SET latest_session_id = NULL, updated_at = now()
WHERE latest_session_id = ?;

-- name: DeleteAICCSession :exec
DELETE FROM aicc_sessions
WHERE id = ?;
