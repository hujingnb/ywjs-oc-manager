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
