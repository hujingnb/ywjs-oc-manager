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
    image_object_key, image_mime, image_size_bytes, hermes_message_id, client_message_id, reply_to_message_id,
    is_fallback, is_refusal, error_summary
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetAICCMessageByClientMessageID :one
SELECT *
FROM aicc_messages
WHERE session_id = ? AND direction = ? AND client_message_id = ?;

-- name: GetAICCAssistantMessageByVisitorMessageID :one
-- dispatcher 写入 reply_to_message_id，使无 client_message_id 的公开消息也能准确定位助手回复。
SELECT *
FROM aicc_messages
WHERE reply_to_message_id = ? AND direction = 'assistant';

-- name: GetAICCMessageByID :one
-- dispatcher 仅按任务关联的访客消息读取原文，避免重新按客户端幂等键查询。
SELECT *
FROM aicc_messages
WHERE id = ?;

-- name: CreateAICCMessageTask :exec
INSERT INTO aicc_message_tasks (
    id, message_id, session_id, agent_id, org_id, app_id, run_after
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: LockAICCQueueGovernance :one
-- 全局单行锁只覆盖 admission 检查与消息/任务写入，确保所有 manager 副本观察同一容量事实。
SELECT id FROM aicc_queue_governance WHERE id = 1 FOR UPDATE;

-- name: CountActiveAICCMessageTasks :one
-- completed/failed 已释放队列名额；queued、retry_wait 与 processing 都仍占用容量。
SELECT COUNT(*) FROM aicc_message_tasks WHERE status NOT IN ('completed', 'failed');

-- name: GetAICCMessageTaskByMessageID :one
SELECT *
FROM aicc_message_tasks
WHERE message_id = ?;

-- name: RequeueFailedAICCMessageTask :execrows
-- 访客显式重试仅能恢复终态失败任务；重置尝试计数后 dispatcher 才可按既有领取条件重新执行。
UPDATE aicc_message_tasks
SET status = 'queued',
    attempts = 0,
    run_after = NOW(6),
    lease_token = NULL,
    lease_expires_at = NULL,
    last_error = NULL,
    updated_at = NOW(6)
WHERE message_id = ? AND status = 'failed';

-- name: ListReadyAICCMessageTasks :many
-- 只返回当前到期且所在会话没有执行中任务的任务；租约时仍会再次原子校验，列表结果不能视作所有权。
SELECT task.*
FROM aicc_message_tasks AS task
JOIN aicc_messages AS task_message ON task_message.id = task.message_id
WHERE task.status IN ('queued', 'retry_wait')
  AND task.attempts < task.max_attempts
  AND task.run_after <= NOW(6)
  AND NOT EXISTS (
      SELECT 1
      FROM aicc_message_tasks AS processing
      WHERE processing.session_id = task.session_id
        AND processing.status = 'processing'
  )
  AND NOT EXISTS (
      -- 同会话只允许最早的未终态访客消息进入候选集，不能依赖 goroutine 或 Redis 信号顺序。
      SELECT 1
      FROM aicc_message_tasks AS earlier
      JOIN aicc_messages AS earlier_message ON earlier_message.id = earlier.message_id
      WHERE earlier.session_id = task.session_id
        AND earlier.status NOT IN ('completed', 'failed')
        AND (earlier_message.created_at < task_message.created_at
             OR (earlier_message.created_at = task_message.created_at AND earlier_message.id < task_message.id))
  )
ORDER BY task.run_after ASC, task_message.created_at ASC, task_message.id ASC
LIMIT ?;

-- name: CountReadyAICCMessageTasksByApp :many
-- 与 ListReadyAICCMessageTasks 使用完全相同的可领取条件，但不带分派 LIMIT；
-- 该分组真值专供 app 级 HPA queue gauge，不能从单轮领取候选集推断。
SELECT task.app_id, COUNT(*) AS queue_depth
FROM aicc_message_tasks AS task
JOIN aicc_messages AS task_message ON task_message.id = task.message_id
WHERE task.status IN ('queued', 'retry_wait')
  AND task.attempts < task.max_attempts
  AND task.run_after <= NOW(6)
  AND NOT EXISTS (
      SELECT 1
      FROM aicc_message_tasks AS processing
      WHERE processing.session_id = task.session_id
        AND processing.status = 'processing'
  )
  AND NOT EXISTS (
      SELECT 1
      FROM aicc_message_tasks AS earlier
      JOIN aicc_messages AS earlier_message ON earlier_message.id = earlier.message_id
      WHERE earlier.session_id = task.session_id
        AND earlier.status NOT IN ('completed', 'failed')
        AND (earlier_message.created_at < task_message.created_at
             OR (earlier_message.created_at = task_message.created_at AND earlier_message.id < task_message.id))
  )
GROUP BY task.app_id;

-- name: LeaseAICCMessageTask :execrows
-- status、尝试上限、到期时间和会话无 processing 任务均在同一 UPDATE 中判断，避免 dispatcher 先读后写造成重复租约。
UPDATE aicc_message_tasks AS task
JOIN aicc_messages AS task_message ON task_message.id = task.message_id
LEFT JOIN aicc_message_tasks AS processing
  ON processing.session_id = task.session_id
 AND processing.status = 'processing'
LEFT JOIN (
    aicc_message_tasks AS earlier
    JOIN aicc_messages AS earlier_message ON earlier_message.id = earlier.message_id
) ON earlier.session_id = task.session_id
 AND earlier.status NOT IN ('completed', 'failed')
 AND (earlier_message.created_at < task_message.created_at
      OR (earlier_message.created_at = task_message.created_at AND earlier_message.id < task_message.id))
SET task.status = 'processing',
    task.lease_token = sqlc.arg(lease_token),
    -- 初次领取与续租都以数据库时间计算，避免 worker 时钟漂移产生错误过期时间。
    task.lease_expires_at = DATE_ADD(NOW(6), INTERVAL 30 SECOND),
    task.attempts = task.attempts + 1,
    task.updated_at = NOW(6)
WHERE task.id = sqlc.arg(id)
  AND task.status IN ('queued', 'retry_wait')
  AND task.attempts < task.max_attempts
  AND task.run_after <= NOW(6)
  AND processing.id IS NULL
  AND earlier.id IS NULL;

-- name: CompleteAICCMessageTask :execrows
UPDATE aicc_message_tasks
SET status = 'completed', lease_token = NULL, lease_expires_at = NULL, updated_at = NOW(6)
WHERE id = sqlc.arg(id) AND status = 'processing' AND lease_token = sqlc.arg(lease_token);

-- name: RenewAICCMessageTaskLease :execrows
-- 续租使用数据库当前时间，避免 worker 时钟漂移把有效租约提前判过期。
UPDATE aicc_message_tasks
SET lease_expires_at = DATE_ADD(NOW(6), INTERVAL 30 SECOND), updated_at = NOW(6)
WHERE id = sqlc.arg(id) AND status = 'processing' AND lease_token = sqlc.arg(lease_token);

-- name: RetryAICCMessageTask :execrows
-- 重试请求会在同一更新内判定上限；最后一次失败直接终态化，且继续记录 worker 返回的错误摘要。
UPDATE aicc_message_tasks
SET status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'retry_wait' END,
    run_after = CASE WHEN attempts >= max_attempts THEN run_after ELSE sqlc.arg(run_after) END,
    lease_token = NULL,
    lease_expires_at = NULL,
    last_error = sqlc.narg(last_error),
    updated_at = NOW(6)
WHERE id = sqlc.arg(id) AND status = 'processing' AND lease_token = sqlc.arg(lease_token);

-- name: FailAICCMessageTask :execrows
UPDATE aicc_message_tasks
SET status = 'failed',
    lease_token = NULL,
    lease_expires_at = NULL,
    last_error = sqlc.narg(last_error),
    updated_at = NOW(6)
WHERE id = sqlc.arg(id) AND status = 'processing' AND lease_token = sqlc.arg(lease_token);

-- name: RecoverExpiredAICCMessageTaskLeases :execrows
-- reaper 仅接管过期租约；耗尽尝试次数的任务转为 failed，其他任务立即回到 retry_wait。
UPDATE aicc_message_tasks
SET status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'retry_wait' END,
    run_after = CASE WHEN attempts >= max_attempts THEN run_after ELSE NOW(6) END,
    lease_token = NULL,
    lease_expires_at = NULL,
    last_error = COALESCE(last_error, 'processing lease expired'),
    updated_at = NOW(6)
WHERE status = 'processing' AND lease_expires_at <= NOW(6);

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

-- name: GetAICCSessionContext :one
SELECT *
FROM aicc_session_contexts
WHERE session_id = ?;

-- name: UpsertAICCSessionContext :exec
INSERT INTO aicc_session_contexts (
    id, session_id, summary, summarized_through_message_id, summary_version
) VALUES (?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    summary = VALUES(summary),
    summarized_through_message_id = VALUES(summarized_through_message_id),
    summary_version = VALUES(summary_version),
    updated_at = now();

-- name: ListAICCContextMessages :many
-- 上下文构建器从稳定升序消息流中截取最近窗口，不能依赖数据库未声明的自然顺序。
SELECT *
FROM aicc_messages
WHERE session_id = sqlc.arg(session_id)
  AND id <> sqlc.arg(exclude_message_id)
ORDER BY created_at ASC, id ASC;

-- name: CreateAICCMessageSource :exec
INSERT INTO aicc_message_sources (
    id, message_id, source_type, title, url, scope, reference_id, unconfirmed, retrieved_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListAICCMessageSources :many
SELECT *
FROM aicc_message_sources
WHERE message_id = ?
ORDER BY created_at ASC, id ASC;

-- name: GetAICCSessionIntent :one
SELECT *
FROM aicc_session_intents
WHERE session_id = ?;

-- name: UpsertAICCSessionIntent :exec
INSERT INTO aicc_session_intents (
    id, session_id, intent_level, fields_json, confidence_json, evidence_json,
    analyzer_version, analyzed_message_id, invite_status
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    intent_level = VALUES(intent_level),
    fields_json = VALUES(fields_json),
    confidence_json = VALUES(confidence_json),
    evidence_json = VALUES(evidence_json),
    analyzer_version = VALUES(analyzer_version),
    -- 首次邀约已展示后保留对应访客消息，公开端据此只在那条助手回复上展示留资卡片。
    analyzed_message_id = IF(invite_status = 'not_invited', VALUES(analyzed_message_id), analyzed_message_id),
    -- 画像重试可能晚于访客拒绝/提交；只能更新画像，绝不能回写已消费的邀约状态。
    updated_at = now();

-- name: UpdateAICCSessionIntentInviteStatus :execrows
-- 访客的拒绝或显式提交只改变本会话的邀约状态，绝不能影响其它匿名会话。
UPDATE aicc_session_intents
SET invite_status = ?, updated_at = now()
WHERE session_id = ? AND invite_status = 'invited';

-- name: ConsumeAICCSessionIntentInvitation :execrows
-- 首次邀约只能从 not_invited 原子推进到 invited，不能覆盖访客已拒绝或已提交的决定。
UPDATE aicc_session_intents
SET invite_status = 'invited', updated_at = now()
WHERE session_id = ? AND invite_status = 'not_invited';

-- name: UpsertAICCIntentAnalysisRetry :exec
INSERT INTO aicc_intent_analysis_retries (session_id, message_id, attempts, run_after, last_error)
VALUES (?, ?, 1, DATE_ADD(NOW(6), INTERVAL 1 SECOND), ?)
ON DUPLICATE KEY UPDATE
    message_id = VALUES(message_id), attempts = attempts + 1,
    run_after = DATE_ADD(NOW(6), INTERVAL LEAST(attempts, 5) SECOND), last_error = VALUES(last_error),
    -- cleanup 留下 processed 记录时，新的主回复分析失败必须能重新排队；未处理/已领取记录不重置。
    lease_token = IF(processed_at IS NOT NULL, NULL, lease_token),
    lease_expires_at = IF(processed_at IS NOT NULL, NULL, lease_expires_at),
    processed_at = IF(processed_at IS NOT NULL, NULL, processed_at);

-- name: ListReadyAICCIntentAnalysisRetries :many
SELECT r.session_id, r.message_id, s.agent_id, s.org_id, a.app_id
FROM aicc_intent_analysis_retries r
JOIN aicc_sessions s ON s.id = r.session_id
JOIN aicc_agents a ON a.id = s.agent_id
WHERE r.run_after <= NOW(6)
  AND r.processed_at IS NULL
ORDER BY r.run_after ASC, r.session_id ASC
LIMIT ?;

-- name: ClaimAICCIntentAnalysisRetry :execrows
UPDATE aicc_intent_analysis_retries
-- 意向分析可能等待上游模型，租约需覆盖主请求超时和一次网络抖动，避免 30 秒后重复分析。
SET lease_token = ?, lease_expires_at = DATE_ADD(NOW(6), INTERVAL 5 MINUTE)
WHERE session_id = ? AND message_id = ?
  AND processed_at IS NULL
  AND (lease_expires_at IS NULL OR lease_expires_at < NOW(6));

-- name: MarkAICCIntentAnalysisRetryProcessed :execrows
UPDATE aicc_intent_analysis_retries
SET processed_at = NOW(), lease_token = NULL, lease_expires_at = NULL
WHERE session_id = ? AND message_id = ? AND lease_token = ? AND processed_at IS NULL;

-- name: RescheduleClaimedAICCIntentAnalysisRetry :execrows
UPDATE aicc_intent_analysis_retries
SET attempts = attempts + 1,
    run_after = DATE_ADD(NOW(6), INTERVAL LEAST(attempts, 5) SECOND),
    last_error = ?, lease_token = NULL, lease_expires_at = NULL
WHERE session_id = ? AND message_id = ? AND lease_token = ? AND processed_at IS NULL;

-- name: DeleteProcessedAICCIntentAnalysisRetry :execrows
DELETE FROM aicc_intent_analysis_retries
WHERE session_id = ? AND message_id = ? AND processed_at IS NOT NULL;

-- name: DeleteAICCIntentAnalysisRetry :exec
DELETE FROM aicc_intent_analysis_retries WHERE session_id = ? AND message_id = ?;

-- name: ListAICCAnonymousIntentCandidates :many
-- 已关联正式线索的会话不再作为匿名候选，避免后台对同一客户出现两份待跟进记录。
SELECT i.*
FROM aicc_session_intents i
JOIN aicc_sessions s ON s.id = i.session_id
WHERE s.org_id = ?
  AND i.intent_level = 'high'
  AND NOT EXISTS (
      SELECT 1
      FROM aicc_lead_values v
      WHERE v.session_id = i.session_id AND v.lead_id IS NOT NULL
  )
ORDER BY i.created_at ASC, i.id ASC;

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

-- name: ResetAICCSessionResolutionForNewPhase :exec
-- 仅在访客已明确选择结果后写入阶段起点；未知状态下的追问不能伪造新阶段。
UPDATE aicc_sessions
SET resolution_status = 'unknown', resolution_phase_start_message_id = ?, updated_at = now()
WHERE id = ? AND resolution_status IN ('resolved', 'unresolved');

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
