package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/store/sqlc"
)

type aiccDispatcherStoreFake struct {
	task      sqlc.AiccMessageTask
	visitor   sqlc.AiccMessage
	agent     sqlc.AiccAgent
	leased    sqlc.LeaseAICCMessageTaskParams
	retry     sqlc.RetryAICCMessageTaskParams
	failed    sqlc.FailAICCMessageTaskParams
	assistant []sqlc.CreateAICCMessageParams
	complete  int64
	recover   int64
	leaseRows int64
	leaseErr  error
	unclaimed bool
}

func (s *aiccDispatcherStoreFake) GetAICCMessageByID(context.Context, string) (sqlc.AiccMessage, error) {
	return s.visitor, nil
}
func (s *aiccDispatcherStoreFake) GetAICCAgent(context.Context, string) (sqlc.AiccAgent, error) {
	return s.agent, nil
}
func (s *aiccDispatcherStoreFake) LeaseAICCMessageTask(_ context.Context, p sqlc.LeaseAICCMessageTaskParams) (int64, error) {
	s.leased = p
	if !s.unclaimed && s.leaseRows == 0 && s.leaseErr == nil {
		return 1, nil
	}
	return s.leaseRows, s.leaseErr
}
func (s *aiccDispatcherStoreFake) CompleteAICCMessageTask(context.Context, sqlc.CompleteAICCMessageTaskParams) (int64, error) {
	return s.complete, nil
}
func (s *aiccDispatcherStoreFake) RetryAICCMessageTask(_ context.Context, p sqlc.RetryAICCMessageTaskParams) (int64, error) {
	s.retry = p
	return 1, nil
}
func (s *aiccDispatcherStoreFake) FailAICCMessageTask(_ context.Context, p sqlc.FailAICCMessageTaskParams) (int64, error) {
	s.failed = p
	return 1, nil
}
func (s *aiccDispatcherStoreFake) RecoverExpiredAICCMessageTaskLeases(context.Context) (int64, error) {
	return s.recover, nil
}
func (s *aiccDispatcherStoreFake) CreateAICCMessage(_ context.Context, p sqlc.CreateAICCMessageParams) error {
	s.assistant = append(s.assistant, p)
	return nil
}

type aiccDispatcherChatFake struct {
	reply string
	err   error
}

func (c aiccDispatcherChatFake) ChatAICC(context.Context, string, string, string) (string, error) {
	return c.reply, c.err
}

type aiccDispatcherTxFake struct{ store *aiccDispatcherStoreFake }

func (t aiccDispatcherTxFake) WithAICCDispatcherTx(ctx context.Context, fn func(AICCDispatcherStore) error) error {
	return fn(t.store)
}

func newAICCDispatcherStoreFake() *aiccDispatcherStoreFake {
	return &aiccDispatcherStoreFake{task: sqlc.AiccMessageTask{ID: "task", MessageID: "visitor", SessionID: "session", AgentID: "agent", OrgID: "org", AppID: "app", Attempts: 0, MaxAttempts: 5}, visitor: sqlc.AiccMessage{ID: "visitor", TextContent: null.StringFrom("价格")}, agent: sqlc.AiccAgent{ID: "agent"}, complete: 1}
}

// TestAICCDispatcherLeaseUsesThirtySecondWindow 覆盖 dispatcher 领取任务时的租约边界：
// 运行中的上游调用必须拥有固定 30 秒所有权，避免多个 worker 重复执行同一访客消息。
func TestAICCDispatcherLeaseUsesThirtySecondWindow(t *testing.T) {
	s := newAICCDispatcherStoreFake()
	d := NewAICCDispatcher(s, aiccDispatcherTxFake{s}, aiccDispatcherChatFake{reply: "答复"}, nil)
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return now }
	require.NoError(t, d.Dispatch(context.Background(), s.task))
	assert.Equal(t, now.Add(30*time.Second), s.leased.LeaseExpiresAt.Time)
}

// TestAICCDispatcherSuccessAtomicallyWritesAssistantAndCompletes 覆盖成功闭环：
// 助手镜像与任务完成由同一个事务回调写入，刷新页面不会看到已完成但缺少回复。
func TestAICCDispatcherSuccessAtomicallyWritesAssistantAndCompletes(t *testing.T) {
	s := newAICCDispatcherStoreFake()
	d := NewAICCDispatcher(s, aiccDispatcherTxFake{s}, aiccDispatcherChatFake{reply: "答复"}, nil)
	require.NoError(t, d.Dispatch(context.Background(), s.task))
	require.Len(t, s.assistant, 1)
	assert.Equal(t, "答复", s.assistant[0].TextContent.String)
}

// TestAICCDispatcherRetryableErrorsEnterRetryWait 覆盖上游限流、服务不可用、网关过载和超时：
// 这些短暂故障必须进入任务重试状态而不是丢失访客消息。
func TestAICCDispatcherRetryableErrorsEnterRetryWait(t *testing.T) {
	for _, err := range []error{&AICCUpstreamStatusError{StatusCode: 429}, &AICCUpstreamStatusError{StatusCode: 503}, &AICCUpstreamStatusError{StatusCode: 529}, context.DeadlineExceeded} {
		s := newAICCDispatcherStoreFake()
		d := NewAICCDispatcher(s, aiccDispatcherTxFake{s}, aiccDispatcherChatFake{err: err}, nil)
		require.NoError(t, d.Dispatch(context.Background(), s.task))
		assert.Equal(t, 2*time.Second, time.Until(s.retry.RunAfter).Round(time.Second))
	}
}

// TestAICCDispatcherDeterministicErrorFails 覆盖非暂态运行时错误：确定性失败不得无限消耗重试配额。
func TestAICCDispatcherDeterministicErrorFails(t *testing.T) {
	s := newAICCDispatcherStoreFake()
	d := NewAICCDispatcher(s, aiccDispatcherTxFake{s}, aiccDispatcherChatFake{err: errors.New("invalid runtime request")}, nil)
	require.NoError(t, d.Dispatch(context.Background(), s.task))
	assert.Equal(t, "invalid runtime request", s.failed.LastError.String)
}

// TestAICCDispatcherFifthRetryKeepsQueryTerminalSemantics 覆盖第五次暂态失败：
// dispatcher 仍调用原子 Retry 查询，由 SQL 根据 attempts/max_attempts 终态化，不能在 service 层绕过该约束。
func TestAICCDispatcherFifthRetryKeepsQueryTerminalSemantics(t *testing.T) {
	s := newAICCDispatcherStoreFake()
	s.task.Attempts = 4
	d := NewAICCDispatcher(s, aiccDispatcherTxFake{s}, aiccDispatcherChatFake{err: &AICCUpstreamStatusError{StatusCode: 503}}, nil)

	require.NoError(t, d.Dispatch(context.Background(), s.task))
	assert.Equal(t, "task", s.retry.ID)
	delay := s.retry.RunAfter.Sub(time.Now())
	assert.GreaterOrEqual(t, delay, 40*time.Second)
	assert.Less(t, delay, 41*time.Second)
}

// TestAICCDispatcherRequiresTransactionRunner 覆盖成功写入的事务边界：
// 未提供事务 runner 时 dispatcher 必须在领取任务前失败，不能产生半提交的助手消息。
func TestAICCDispatcherRequiresTransactionRunner(t *testing.T) {
	s := newAICCDispatcherStoreFake()
	d := NewAICCDispatcher(s, nil, aiccDispatcherChatFake{reply: "答复"}, nil)

	require.Error(t, d.Dispatch(context.Background(), s.task))
	assert.Empty(t, s.leased.ID)
	assert.Empty(t, s.assistant)
}

// TestAICCDispatcherReleasesHalfOpenProbeWhenTaskNotClaimed 覆盖半开探测无可领取任务：
// 租约竞争失败后必须释放探测权，后续到期任务才能继续尝试领取。
func TestAICCDispatcherReleasesHalfOpenProbeWhenTaskNotClaimed(t *testing.T) {
	s := newAICCDispatcherStoreFake()
	s.unclaimed = true
	d := NewAICCDispatcher(s, aiccDispatcherTxFake{s}, aiccDispatcherChatFake{reply: "答复"}, nil)
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	current := now
	d.now = func() time.Time { return current }
	for range 5 {
		d.recordOverload(now)
	}

	current = now.Add(30 * time.Second)
	require.NoError(t, d.Dispatch(context.Background(), s.task))
	assert.True(t, d.allow(current))
}

// TestAICCDispatcherCircuitHalfOpenSuccessRecovers 覆盖连续五次过载后的熔断和半开恢复：
// 冷却期内不再领取任务，30 秒后允许一次探测；探测成功后恢复正常调度。
func TestAICCDispatcherCircuitHalfOpenSuccessRecovers(t *testing.T) {
	s := newAICCDispatcherStoreFake()
	d := NewAICCDispatcher(s, aiccDispatcherTxFake{s}, aiccDispatcherChatFake{reply: "答复"}, nil)
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return now }
	for range 5 {
		d.recordOverload(now)
	}
	assert.False(t, d.allow(now.Add(time.Second)))
	assert.True(t, d.allow(now.Add(30*time.Second)))
	assert.False(t, d.allow(now.Add(30*time.Second)))
	d.recordSuccess()
	assert.True(t, d.allow(now.Add(31*time.Second)))
}

// TestAICCDispatcherRecoverExpiredLeases 覆盖 worker 意外退出后的租约回收，后续扫库可再次调度任务。
func TestAICCDispatcherRecoverExpiredLeases(t *testing.T) {
	s := newAICCDispatcherStoreFake()
	s.recover = 1
	d := NewAICCDispatcher(s, nil, aiccDispatcherChatFake{}, nil)
	n, err := d.RecoverExpiredLeases(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
}
