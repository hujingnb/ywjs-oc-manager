package sqlc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAICCMessageTaskQueriesStopAtMaxAttempts 验证任务租约与重试都在同一条更新语句中检查尝试上限，
// 避免已耗尽次数的消息因并发或调用方误用持续回到 retry_wait。
func TestAICCMessageTaskQueriesStopAtMaxAttempts(t *testing.T) {
	// 租约前必须拒绝已耗尽次数的任务，不能让 attempts 在 max_attempts 之后继续增长。
	assert.Contains(t, normalizedSQL(leaseAICCMessageTask), "task.attempts < task.max_attempts")
	// 初次租约必须使用数据库 NOW(6) 计算，避免 worker 本地时钟漂移造成过早回收或重复执行。
	assert.Contains(t, normalizedSQL(leaseAICCMessageTask), "lease_expires_at = date_add(now(6), interval 30 second)")

	// worker 请求重试时，达到边界的 processing 任务必须直接结束为 failed，并保留本次错误信息。
	assert.Contains(t, normalizedSQL(retryAICCMessageTask), "case when attempts >= max_attempts then 'failed' else 'retry_wait' end")
	assert.Contains(t, normalizedSQL(retryAICCMessageTask), "last_error = ?")
}

// TestAICCMessageTaskLeaseRequiresEarliestSessionMessage 验证同会话后续任务不能绕过更早的持久化消息取得租约。
func TestAICCMessageTaskLeaseRequiresEarliestSessionMessage(t *testing.T) {
	query := normalizedSQL(leaseAICCMessageTask)
	assert.Contains(t, query, "join aicc_messages as task_message")
	assert.Contains(t, query, "earlier_message.created_at < task_message.created_at")
}

// TestCountReadyAICCMessageTasksByAppExcludesExhaustedTasks 验证 HPA queue gauge 与实际
// 租约边界一致：queued/retry_wait 状态但已耗尽尝试次数的任务不能再计入任何应用积压。
func TestCountReadyAICCMessageTasksByAppExcludesExhaustedTasks(t *testing.T) {
	query := normalizedSQL(countReadyAICCMessageTasksByApp)
	assert.Contains(t, query, "task.attempts < task.max_attempts")
}

// TestRequeueFailedAICCMessageTaskOnlyResetsTerminalFailure 验证访客手动重试只恢复 failed 任务，
// 并清空已耗尽的尝试计数和错误摘要，让 dispatcher 可按原有领取规则重新执行。
func TestRequeueFailedAICCMessageTaskOnlyResetsTerminalFailure(t *testing.T) {
	query := normalizedSQL(requeueFailedAICCMessageTask)
	assert.Contains(t, query, "set status = 'queued'")
	assert.Contains(t, query, "attempts = 0")
	assert.Contains(t, query, "last_error = null")
	assert.Contains(t, query, "where message_id = ? and status = 'failed'")
}

// TestAICCConversationIntelligenceQueriesUseStableOrdering 验证上下文、来源和匿名意向候选
// 都显式使用时间与主键排序，避免同一时间写入的记录在不同数据库执行计划下顺序漂移。
func TestAICCConversationIntelligenceQueriesUseStableOrdering(t *testing.T) {
	// 上下文构建器需要按完整会话消息流截取最近窗口，升序结果可直接保留自然对话顺序。
	assert.Contains(t, normalizedSQL(listAICCContextMessages), "order by created_at asc, id asc")
	// 一条回复的多条引用必须稳定显示，防止相同来源在前端每次刷新时重排。
	assert.Contains(t, normalizedSQL(listAICCMessageSources), "order by created_at asc, id asc")
	// 匿名候选排除已形成正式线索的会话，并以可复现顺序供后台分页或导出。
	candidates := normalizedSQL(listAICCAnonymousIntentCandidates)
	assert.Contains(t, candidates, "where s.org_id = ?")
	assert.Contains(t, candidates, "v.lead_id is not null")
	assert.Contains(t, candidates, "order by i.created_at asc, i.id asc")
}

// TestAICCConversationIntelligenceQueriesKeepSessionAndMessageIsolation 验证摘要、原始消息、
// 意向画像及其来源均以 session 或 message 主键精确过滤，不能在无状态续聊时串入其它访客数据。
func TestAICCConversationIntelligenceQueriesKeepSessionAndMessageIsolation(t *testing.T) {
	// 正常路径：指定 session 后才能读取其摘要与已分析意向，查询不应接受空的全局筛选条件。
	assert.Contains(t, normalizedSQL(getAICCSessionContext), "from aicc_session_contexts where session_id = ?")
	assert.Contains(t, normalizedSQL(getAICCSessionIntent), "from aicc_session_intents where session_id = ?")
	// 边界路径：同一时间写入的多条消息仍限定在一个 session，不能因排序窗口混入其它会话。
	contextMessages := normalizedSQL(listAICCContextMessages)
	assert.Contains(t, contextMessages, "from aicc_messages where session_id = ?")
	assert.NotContains(t, contextMessages, "join aicc_sessions")
	// 每个来源只由对应助手消息读取，调用方无法通过来源查询枚举其它消息的引用。
	assert.Contains(t, normalizedSQL(listAICCMessageSources), "from aicc_message_sources where message_id = ?")
}

// TestAICCConversationIntelligenceUpsertsReplaceSingleSessionFact 验证重复分析同一会话时
// 会更新既有摘要或意向，而不是创建第二行；来源则按单条消息写入多个独立证据。
func TestAICCConversationIntelligenceUpsertsReplaceSingleSessionFact(t *testing.T) {
	// 正常路径：摘要写入覆盖正文、处理水位和版本，使下一个新 Pod 使用最新事实恢复上下文。
	context := normalizedSQL(upsertAICCSessionContext)
	assert.Contains(t, context, "insert into aicc_session_contexts")
	assert.Contains(t, context, "on duplicate key update")
	assert.Contains(t, context, "summary = values(summary)")
	assert.Contains(t, context, "summarized_through_message_id = values(summarized_through_message_id)")
	assert.Contains(t, context, "summary_version = values(summary_version)")
	// 边界路径：意向重新分析必须同步覆盖 JSON 证据、分析版本和邀请状态，不能遗留旧轮次判断。
	intent := normalizedSQL(upsertAICCSessionIntent)
	assert.Contains(t, intent, "insert into aicc_session_intents")
	assert.Contains(t, intent, "fields_json = values(fields_json)")
	assert.Contains(t, intent, "confidence_json = values(confidence_json)")
	assert.Contains(t, intent, "evidence_json = values(evidence_json)")
	assert.Contains(t, intent, "analyzed_message_id = values(analyzed_message_id)")
	assert.NotContains(t, intent, "invite_status = values(invite_status)")
	// 一条助手消息可以保留多条工具审计来源，因此创建查询不得错误使用 upsert 或遗漏未确认标记。
	source := normalizedSQL(createAICCMessageSource)
	assert.Contains(t, source, "insert into aicc_message_sources")
	assert.Contains(t, source, "source_type, title, url, scope, reference_id, unconfirmed, retrieved_at")
	assert.NotContains(t, source, "on duplicate key update")
}
