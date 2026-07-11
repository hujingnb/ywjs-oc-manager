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

-- name: GetAICCAgentByWidgetToken :one
SELECT *
FROM aicc_agents
WHERE widget_token = ? AND status = 'active' AND deleted_at IS NULL;

-- name: GetAICCAgentByAppID :one
SELECT *
FROM aicc_agents
WHERE app_id = ? AND deleted_at IS NULL;

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

-- name: DeleteAICCAgentIndustryKnowledgeNotAuthorizedByOrg :exec
-- 平台撤销企业行业库授权时，立即移除该企业全部智能体的失效行业库关联，避免历史配置继续参与检索。
DELETE ak
FROM aicc_agent_knowledge ak
JOIN aicc_agents aa
  ON aa.id = ak.agent_id
 AND aa.org_id = ak.agent_org_id
WHERE aa.org_id = sqlc.arg(org_id)
  AND ak.scope_type = 'industry'
  AND NOT EXISTS (
    SELECT 1
    FROM organization_industry_knowledge_bases oikb
    WHERE oikb.org_id = aa.org_id
      AND oikb.industry_knowledge_base_id = ak.industry_knowledge_base_id
  );

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
SELECT s.*,
       (SELECT COUNT(*) FROM aicc_messages m WHERE m.session_id = s.id) AS message_count
FROM aicc_sessions s
WHERE s.agent_id = ?
  AND EXISTS (SELECT 1 FROM aicc_messages m WHERE m.session_id = s.id)
  AND (sqlc.narg(resolution_status) IS NULL OR s.resolution_status = sqlc.narg(resolution_status))
  AND (sqlc.narg(lead_status) IS NULL OR s.lead_status = sqlc.narg(lead_status))
  AND (sqlc.narg(channel) IS NULL OR s.channel = sqlc.narg(channel))
  AND (sqlc.narg(region) IS NULL OR s.region = sqlc.narg(region))
  AND (sqlc.narg(start_at) IS NULL OR s.created_at >= sqlc.narg(start_at))
  AND (sqlc.narg(end_at) IS NULL OR s.created_at < sqlc.narg(end_at))
  AND (
      sqlc.narg(keyword) IS NULL
      OR s.source_url LIKE CONCAT('%', sqlc.narg(keyword), '%')
      OR s.referrer LIKE CONCAT('%', sqlc.narg(keyword), '%')
  )
ORDER BY s.created_at DESC, s.id DESC
LIMIT ? OFFSET ?;

-- name: CountAICCSessionsByAgent :one
SELECT COUNT(*)
FROM aicc_sessions s
WHERE s.agent_id = ?
  AND EXISTS (SELECT 1 FROM aicc_messages m WHERE m.session_id = s.id)
  AND (sqlc.narg(resolution_status) IS NULL OR s.resolution_status = sqlc.narg(resolution_status))
  AND (sqlc.narg(lead_status) IS NULL OR s.lead_status = sqlc.narg(lead_status))
  AND (sqlc.narg(channel) IS NULL OR s.channel = sqlc.narg(channel))
  AND (sqlc.narg(region) IS NULL OR s.region = sqlc.narg(region))
  AND (sqlc.narg(start_at) IS NULL OR s.created_at >= sqlc.narg(start_at))
  AND (sqlc.narg(end_at) IS NULL OR s.created_at < sqlc.narg(end_at))
  AND (
      sqlc.narg(keyword) IS NULL
      OR s.source_url LIKE CONCAT('%', sqlc.narg(keyword), '%')
      OR s.referrer LIKE CONCAT('%', sqlc.narg(keyword), '%')
  );

-- name: GetAICCSession :one
SELECT *
FROM aicc_sessions
WHERE id = ?;

-- name: LockAICCSessionForUpdate :one
SELECT *
FROM aicc_sessions
WHERE id = ? AND expires_at > now()
FOR UPDATE;

-- name: MarkAICCSessionConsented :execrows
UPDATE aicc_sessions
SET privacy_consented_at = now(), updated_at = now()
WHERE session_token = ? AND expires_at > now();

-- name: TouchAICCSessionLastActive :execrows
UPDATE aicc_sessions
SET last_active_at = IF(last_active_at >= now(), DATE_ADD(last_active_at, INTERVAL 1 SECOND), now()),
    updated_at = now()
WHERE id = ? AND expires_at > now();

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

-- name: ListAICCLeadValuesBySession :many
SELECT
    v.lead_id,
    v.session_id,
    v.field_id,
    f.field_key,
    f.label,
    f.field_type,
    v.value_text,
    v.created_at
FROM aicc_lead_values v
JOIN aicc_lead_fields f ON f.id = v.field_id AND f.agent_id = v.agent_id
WHERE v.session_id = ?
ORDER BY f.sort_order ASC, f.id ASC;

-- name: ListAICCLeadValuesByLead :many
SELECT
    v.lead_id,
    v.session_id,
    v.field_id,
    f.field_key,
    f.label,
    f.field_type,
    v.value_text,
    v.created_at
FROM aicc_lead_values v
JOIN aicc_lead_fields f ON f.id = v.field_id AND f.agent_id = v.agent_id
WHERE v.lead_id = ? AND v.lead_org_id = ?
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

-- name: CountAICCCompletedLeadSessionsInRange :one
SELECT COUNT(*)
FROM aicc_sessions
WHERE org_id = ?
  AND (sqlc.narg(agent_id) IS NULL OR agent_id = sqlc.narg(agent_id))
  AND created_at >= ?
  AND created_at < ?
  AND lead_status = 'complete';

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

-- name: ListAICCTopVisitorQuestionsInRange :many
SELECT TRIM(m.text_content) AS question,
       CAST(COUNT(*) AS SIGNED) AS count
FROM aicc_messages m
JOIN aicc_sessions s ON s.id = m.session_id
WHERE s.org_id = ?
  AND (sqlc.narg(agent_id) IS NULL OR s.agent_id = sqlc.narg(agent_id))
  AND s.created_at >= ?
  AND s.created_at < ?
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

-- name: ListAICCTopSourceURLsInRange :many
SELECT source_url,
       CAST(COUNT(*) AS SIGNED) AS count
FROM aicc_sessions
WHERE org_id = ?
  AND (sqlc.narg(agent_id) IS NULL OR agent_id = sqlc.narg(agent_id))
  AND created_at >= ?
  AND created_at < ?
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

-- name: DeleteOrphanAICCLeadsByOrg :exec
DELETE l
FROM aicc_leads l
WHERE l.org_id = ?
  AND l.latest_session_id IS NULL
  AND NOT EXISTS (
      SELECT 1
      FROM aicc_lead_values v
      WHERE v.lead_id = l.id AND v.lead_org_id = l.org_id
  );

-- name: DeleteAICCSession :exec
DELETE FROM aicc_sessions
WHERE id = ?;

-- name: GetAICCAgentSettings :one
SELECT *
FROM aicc_agent_settings
WHERE agent_id = ?;

-- name: UpsertAICCAgentSettings :exec
INSERT INTO aicc_agent_settings (
    agent_id, message_limit_per_session, sensitive_words_json,
    blocked_visitor_enabled, blocked_visitor_threshold_json,
    session_resume_ttl_minutes, analytics_config_json
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    message_limit_per_session = VALUES(message_limit_per_session),
    sensitive_words_json = VALUES(sensitive_words_json),
    blocked_visitor_enabled = VALUES(blocked_visitor_enabled),
    blocked_visitor_threshold_json = VALUES(blocked_visitor_threshold_json),
    session_resume_ttl_minutes = VALUES(session_resume_ttl_minutes),
    updated_at = now();

-- name: ListAICCBlockedVisitorsByAgent :many
SELECT *
FROM aicc_blocked_visitors
WHERE agent_id = ?
ORDER BY created_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: CountActiveAICCBlockedVisitorsByAgent :one
SELECT COUNT(*)
FROM aicc_blocked_visitors
WHERE agent_id = ? AND expires_at > now();

-- name: GetActiveAICCBlockedVisitor :one
SELECT *
FROM aicc_blocked_visitors
WHERE agent_id = ? AND visitor_hash = ? AND expires_at > now()
ORDER BY expires_at DESC, id DESC
LIMIT 1;

-- name: UpsertAICCBlockedVisitor :exec
INSERT INTO aicc_blocked_visitors (
    id, agent_id, org_id, visitor_hash, reason, expires_at
) VALUES (?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    reason = VALUES(reason),
    expires_at = VALUES(expires_at),
    updated_at = now();

-- name: DeleteAICCBlockedVisitor :execrows
DELETE FROM aicc_blocked_visitors
WHERE id = ? AND agent_id = ?;

-- name: CountAICCVisitorMessagesBySession :one
SELECT COUNT(*)
FROM aicc_messages
WHERE session_id = ? AND direction = 'visitor';

-- name: CountAICCSessionsByStatusInRange :one
SELECT
    COUNT(*) AS total_sessions,
    CAST(COALESCE(SUM(CASE WHEN resolution_status = 'resolved' THEN 1 ELSE 0 END), 0) AS SIGNED) AS resolved_sessions,
    CAST(COALESCE(SUM(CASE WHEN resolution_status = 'unresolved' THEN 1 ELSE 0 END), 0) AS SIGNED) AS unresolved_sessions,
    CAST(COALESCE(SUM(CASE WHEN resolution_status = 'unknown' THEN 1 ELSE 0 END), 0) AS SIGNED) AS unknown_sessions
FROM aicc_sessions
WHERE org_id = ?
  AND (sqlc.narg(agent_id) IS NULL OR agent_id = sqlc.narg(agent_id))
  AND created_at >= ?
  AND created_at < ?;

-- name: ListAICCSessionTrendByDay :many
SELECT DATE(created_at) AS bucket, COUNT(*) AS count
FROM aicc_sessions
WHERE org_id = ?
  AND (sqlc.narg(agent_id) IS NULL OR agent_id = sqlc.narg(agent_id))
  AND created_at >= ?
  AND created_at < ?
GROUP BY DATE(created_at)
ORDER BY bucket ASC;

-- name: ListAICCSessionTrendByWeek :many
SELECT DATE_FORMAT(created_at, '%x-W%v') AS bucket, COUNT(*) AS count
FROM aicc_sessions
WHERE org_id = ?
  AND (sqlc.narg(agent_id) IS NULL OR agent_id = sqlc.narg(agent_id))
  AND created_at >= ?
  AND created_at < ?
GROUP BY DATE_FORMAT(created_at, '%x-W%v')
ORDER BY bucket ASC;

-- name: ListAICCRegionsInRange :many
SELECT CAST(COALESCE(NULLIF(region, ''), '未知') AS CHAR) AS label, COUNT(*) AS count
FROM aicc_sessions
WHERE org_id = ?
  AND (sqlc.narg(agent_id) IS NULL OR agent_id = sqlc.narg(agent_id))
  AND created_at >= ?
  AND created_at < ?
GROUP BY CAST(COALESCE(NULLIF(region, ''), '未知') AS CHAR)
ORDER BY count DESC, label ASC
LIMIT ?;
