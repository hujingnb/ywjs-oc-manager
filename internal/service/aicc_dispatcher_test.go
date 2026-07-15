package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

type aiccDispatcherStoreFake struct {
	task            sqlc.AiccMessageTask
	visitor         sqlc.AiccMessage
	agent           sqlc.AiccAgent
	contextRow      sqlc.AiccSessionContext
	contextMessages []sqlc.AiccMessage
	leased          sqlc.LeaseAICCMessageTaskParams
	retry           sqlc.RetryAICCMessageTaskParams
	failed          sqlc.FailAICCMessageTaskParams
	assistant       []sqlc.CreateAICCMessageParams
	complete        int64
	recover         int64
	retryRows       int64
	failRows        int64
	lostRetry       bool
	lostFail        bool
	renews          int
	leaseMu         sync.Mutex
	exclusive       bool
	claimed         bool
	leaseNow        func() time.Time
	expiresAt       time.Time
	leaseCalls      int
	renewed         chan struct{}
	blockRenew      chan struct{}
	leaseRows       int64
	leaseErr        error
	unclaimed       bool
}

func (s *aiccDispatcherStoreFake) GetAICCMessageByID(context.Context, string) (sqlc.AiccMessage, error) {
	return s.visitor, nil
}
func (s *aiccDispatcherStoreFake) GetAICCAgent(context.Context, string) (sqlc.AiccAgent, error) {
	return s.agent, nil
}

// GetAICCSessionContext 模拟当前会话的持久化摘要。
func (s *aiccDispatcherStoreFake) GetAICCSessionContext(context.Context, string) (sqlc.AiccSessionContext, error) {
	return s.contextRow, nil
}

// ListAICCContextMessages 模拟当前会话按稳定顺序读取的原消息。
func (s *aiccDispatcherStoreFake) ListAICCContextMessages(_ context.Context, arg sqlc.ListAICCContextMessagesParams) ([]sqlc.AiccMessage, error) {
	items := make([]sqlc.AiccMessage, 0, len(s.contextMessages))
	for _, message := range s.contextMessages {
		if message.ID != arg.ExcludeMessageID {
			items = append(items, message)
		}
	}
	return items, nil
}
func (s *aiccDispatcherStoreFake) LeaseAICCMessageTask(_ context.Context, p sqlc.LeaseAICCMessageTaskParams) (int64, error) {
	s.leaseMu.Lock()
	defer s.leaseMu.Unlock()
	s.leaseCalls++
	if s.exclusive && s.claimed {
		return 0, nil
	}
	if s.exclusive {
		s.claimed = true
		if s.leaseNow != nil {
			s.expiresAt = s.leaseNow().Add(aiccTaskLeaseDuration)
		}
	}
	s.leased = p
	if !s.unclaimed && s.leaseRows == 0 && s.leaseErr == nil {
		return 1, nil
	}
	return s.leaseRows, s.leaseErr
}
func (s *aiccDispatcherStoreFake) CompleteAICCMessageTask(context.Context, sqlc.CompleteAICCMessageTaskParams) (int64, error) {
	s.leaseMu.Lock()
	s.claimed = false
	s.leaseMu.Unlock()
	return s.complete, nil
}
func (s *aiccDispatcherStoreFake) RetryAICCMessageTask(_ context.Context, p sqlc.RetryAICCMessageTaskParams) (int64, error) {
	s.retry = p
	if s.lostRetry {
		return 0, nil
	}
	if s.retryRows != 0 {
		return s.retryRows, nil
	}
	return 1, nil
}
func (s *aiccDispatcherStoreFake) FailAICCMessageTask(_ context.Context, p sqlc.FailAICCMessageTaskParams) (int64, error) {
	s.failed = p
	if s.lostFail {
		return 0, nil
	}
	if s.failRows != 0 {
		return s.failRows, nil
	}
	return 1, nil
}
func (s *aiccDispatcherStoreFake) RenewAICCMessageTaskLease(ctx context.Context, _ sqlc.RenewAICCMessageTaskLeaseParams) (int64, error) {
	if s.blockRenew != nil {
		select {
		case <-s.blockRenew:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	s.leaseMu.Lock()
	defer s.leaseMu.Unlock()
	s.renews++
	if s.leaseNow != nil {
		s.expiresAt = s.leaseNow().Add(aiccTaskLeaseDuration)
	}
	if s.renewed != nil {
		select {
		case s.renewed <- struct{}{}:
		default:
		}
	}
	return 1, nil
}
func (s *aiccDispatcherStoreFake) RecoverExpiredAICCMessageTaskLeases(context.Context) (int64, error) {
	s.leaseMu.Lock()
	defer s.leaseMu.Unlock()
	if s.exclusive && s.leaseNow != nil && s.claimed && !s.expiresAt.After(s.leaseNow()) {
		s.claimed = false
		return 1, nil
	}
	return s.recover, nil
}
func (s *aiccDispatcherStoreFake) CreateAICCMessage(_ context.Context, p sqlc.CreateAICCMessageParams) error {
	s.assistant = append(s.assistant, p)
	return nil
}

type aiccDispatcherChatFake struct {
	reply string
	err   error
	run   func(context.Context) (string, error)
}

func (c aiccDispatcherChatFake) ChatAICC(ctx context.Context, _ AICCInboundTurn) (AICCResponseEnvelope, error) {
	if c.run != nil {
		reply, err := c.run(ctx)
		return AICCResponseEnvelope{Text: reply}, err
	}
	return AICCResponseEnvelope{Text: c.reply}, c.err
}

// aiccDispatcherTurnChatFake 记录 dispatcher 交给运行时的 Turn，验证无状态恢复输入。
type aiccDispatcherTurnChatFake struct{ turn AICCInboundTurn }

func (c *aiccDispatcherTurnChatFake) ChatAICC(_ context.Context, turn AICCInboundTurn) (AICCResponseEnvelope, error) {
	c.turn = turn
	return AICCResponseEnvelope{Text: "答复"}, nil
}

type aiccDispatcherTxFake struct{ store *aiccDispatcherStoreFake }

// aiccDispatcherAllowLimiter 显式模拟测试环境可获得的运行额度，避免成功路径依赖生产 Redis。
type aiccDispatcherAllowLimiter struct{}

func (aiccDispatcherAllowLimiter) Acquire(context.Context, string, string, string) (func(), error) {
	return func() {}, nil
}

func (t aiccDispatcherTxFake) WithAICCDispatcherTx(ctx context.Context, fn func(AICCDispatcherStore) error) error {
	return fn(t.store)
}

func newAICCDispatcherStoreFake() *aiccDispatcherStoreFake {
	return &aiccDispatcherStoreFake{task: sqlc.AiccMessageTask{ID: "task", MessageID: "visitor", SessionID: "session", AgentID: "agent", OrgID: "org", AppID: "app", Attempts: 0, MaxAttempts: 5}, visitor: sqlc.AiccMessage{ID: "visitor", TextContent: null.StringFrom("价格")}, agent: sqlc.AiccAgent{ID: "agent"}, complete: 1}
}

// TestAICCDispatcherLeaseUsesDatabaseClock 覆盖 dispatcher 领取任务：
// 初次租约过期时间必须由 SQL NOW(6) 写入，worker 只提交任务 ID 与租约 token。
func TestAICCDispatcherLeaseUsesDatabaseClock(t *testing.T) {
	s := newAICCDispatcherStoreFake()
	d := NewAICCDispatcher(s, aiccDispatcherTxFake{s}, aiccDispatcherChatFake{reply: "答复"}, aiccDispatcherAllowLimiter{})
	require.NoError(t, d.Dispatch(context.Background(), s.task))
	assert.Equal(t, "task", s.leased.ID)
	assert.True(t, s.leased.LeaseToken.Valid)
}

// TestAICCDispatcherBuildsTurnFromDatabaseContext 覆盖无状态运行时调用：
// 每轮必须以任务 MessageID 标识，并在租约内从 manager 数据库装配摘要、历史和当前访客原文。
func TestAICCDispatcherBuildsTurnFromDatabaseContext(t *testing.T) {
	s := newAICCDispatcherStoreFake()
	s.contextRow = sqlc.AiccSessionContext{ID: "context", SessionID: "session", Summary: "已咨询企业版"}
	s.contextMessages = []sqlc.AiccMessage{
		// 历史访客消息必须作为普通上下文数据传入。
		{ID: "history", SessionID: "session", Direction: domain.AICCMessageDirectionVisitor, TextContent: null.StringFrom("之前的问题")},
	}
	chat := &aiccDispatcherTurnChatFake{}
	d := NewAICCDispatcher(s, aiccDispatcherTxFake{s}, chat, aiccDispatcherAllowLimiter{})

	require.NoError(t, d.Dispatch(context.Background(), s.task))
	assert.Equal(t, "visitor", chat.turn.TurnID)
	assert.Equal(t, "session", chat.turn.SessionID)
	assert.Equal(t, "价格", chat.turn.Text)
	assert.Equal(t, "已咨询企业版", chat.turn.Context.Summary)
	require.Len(t, chat.turn.Context.Messages, 1)
	assert.Equal(t, "之前的问题", chat.turn.Context.Messages[0].Text)
}

// TestAICCDispatcherHeartbeatKeepsSlowChatLease 覆盖慢速 Hermes 调用：
// 首轮调用持续超过初始租约窗口时，心跳必须持续续租，第二个 dispatcher 不得再次领取同一任务。
func TestAICCDispatcherHeartbeatKeepsSlowChatLease(t *testing.T) {
	s := newAICCDispatcherStoreFake()
	s.exclusive = true
	started := make(chan struct{})
	finish := make(chan struct{})
	oldInterval := aiccLeaseHeartbeatInterval
	aiccLeaseHeartbeatInterval = time.Millisecond
	defer func() { aiccLeaseHeartbeatInterval = oldInterval }()
	d := NewAICCDispatcher(s, aiccDispatcherTxFake{s}, aiccDispatcherChatFake{run: func(ctx context.Context) (string, error) {
		close(started)
		select {
		case <-finish:
			return "答复", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}}, nil)
	done := make(chan error, 1)
	go func() { done <- d.Dispatch(context.Background(), s.task) }()
	<-started
	time.Sleep(5 * time.Millisecond)

	require.NoError(t, d.Dispatch(context.Background(), s.task))
	s.leaseMu.Lock()
	renews := s.renews
	s.leaseMu.Unlock()
	assert.Positive(t, renews)
	close(finish)
	require.NoError(t, <-done)
}

// TestAICCDispatcherHeartbeatPreventsReaperAfterInitialLeaseWindow 覆盖超过初始 30 秒的慢调用：
// 推进可控数据库时钟并触发 reaper 后，心跳已续租的 processing 任务不能被第二个 worker 重新领取。
func TestAICCDispatcherHeartbeatPreventsReaperAfterInitialLeaseWindow(t *testing.T) {
	s := newAICCDispatcherStoreFake()
	s.exclusive = true
	s.renewed = make(chan struct{}, 4)
	started, finish := make(chan struct{}), make(chan struct{})
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	var nowMu sync.Mutex
	s.leaseNow = func() time.Time { nowMu.Lock(); defer nowMu.Unlock(); return now }
	oldInterval := aiccLeaseHeartbeatInterval
	aiccLeaseHeartbeatInterval = time.Millisecond
	defer func() { aiccLeaseHeartbeatInterval = oldInterval }()
	d := NewAICCDispatcher(s, aiccDispatcherTxFake{s}, aiccDispatcherChatFake{run: func(ctx context.Context) (string, error) {
		close(started)
		select {
		case <-finish:
			return "答复", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}}, nil)
	done := make(chan error, 1)
	go func() { done <- d.Dispatch(context.Background(), s.task) }()
	<-started
	<-s.renewed
	nowMu.Lock()
	now = now.Add(31 * time.Second)
	nowMu.Unlock()
	// 等待推进后的续租真正写入新过期时间；只消费通知可能读到推进前积压的信号，导致测试偶发误判租约过期。
	require.Eventually(t, func() bool {
		s.leaseMu.Lock()
		defer s.leaseMu.Unlock()
		return s.expiresAt.After(s.leaseNow())
	}, time.Second, time.Millisecond)

	recovered, err := d.RecoverExpiredLeases(context.Background())
	require.NoError(t, err)
	assert.Zero(t, recovered)
	require.NoError(t, d.Dispatch(context.Background(), s.task))
	assert.Equal(t, 2, s.leaseCalls)
	close(finish)
	require.NoError(t, <-done)
}

// TestAICCDispatcherHeartbeatStopCancelsBlockedRenew 覆盖数据库续租阻塞：
// stop 必须先取消 context，再等待心跳 goroutine，避免 worker 在关闭时死锁。
func TestAICCDispatcherHeartbeatStopCancelsBlockedRenew(t *testing.T) {
	s := newAICCDispatcherStoreFake()
	s.blockRenew = make(chan struct{})
	d := NewAICCDispatcher(s, aiccDispatcherTxFake{s}, aiccDispatcherChatFake{}, nil)
	oldInterval := aiccLeaseHeartbeatInterval
	aiccLeaseHeartbeatInterval = time.Millisecond
	defer func() { aiccLeaseHeartbeatInterval = oldInterval }()
	ctx, cancel := context.WithCancel(context.Background())
	stop := d.startLeaseHeartbeat(ctx, cancel, "task", "token")
	time.Sleep(5 * time.Millisecond)
	done := make(chan error, 1)
	go func() { done <- stop() }()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("续租停止发生死锁")
	}
}

// TestAICCDispatcherSuccessAtomicallyWritesAssistantAndCompletes 覆盖成功闭环：
// 助手镜像与任务完成由同一个事务回调写入，刷新页面不会看到已完成但缺少回复。
func TestAICCDispatcherSuccessAtomicallyWritesAssistantAndCompletes(t *testing.T) {
	s := newAICCDispatcherStoreFake()
	d := NewAICCDispatcher(s, aiccDispatcherTxFake{s}, aiccDispatcherChatFake{reply: "答复"}, aiccDispatcherAllowLimiter{})
	require.NoError(t, d.Dispatch(context.Background(), s.task))
	require.Len(t, s.assistant, 1)
	assert.Equal(t, "答复", s.assistant[0].TextContent.String)
	assert.Equal(t, "visitor", s.assistant[0].ReplyToMessageID.String)
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

// TestAICCDispatcherRetryZeroRowsMeansLeaseLost 覆盖可重试失败时租约已被接管：
// Retry 更新未命中当前 token 不能累计过载次数或伪装为成功重试。
func TestAICCDispatcherRetryZeroRowsMeansLeaseLost(t *testing.T) {
	s := newAICCDispatcherStoreFake()
	s.lostRetry = true
	d := NewAICCDispatcher(s, aiccDispatcherTxFake{s}, aiccDispatcherChatFake{err: &AICCUpstreamStatusError{StatusCode: 503}}, nil)

	err := d.Dispatch(context.Background(), s.task)

	require.ErrorIs(t, err, ErrAICCLeaseLost)
	assert.Zero(t, d.overloads)
}

// TestAICCDispatcherFailZeroRowsMeansLeaseLost 覆盖确定性失败时租约已被接管：
// Fail 更新未命中时当前 worker 不得再宣称已将任务终态化。
func TestAICCDispatcherFailZeroRowsMeansLeaseLost(t *testing.T) {
	s := newAICCDispatcherStoreFake()
	s.lostFail = true
	d := NewAICCDispatcher(s, aiccDispatcherTxFake{s}, aiccDispatcherChatFake{err: errors.New("invalid request")}, nil)

	err := d.Dispatch(context.Background(), s.task)

	require.ErrorIs(t, err, ErrAICCLeaseLost)
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

// TestAICCDispatcherReopensCircuitAfterNonRetryableHalfOpenFailure 覆盖半开探测确定性失败：
// 已领取的探测任务失败后必须重新熔断 30 秒，冷却结束后新的任务仍可被接纳。
func TestAICCDispatcherReopensCircuitAfterNonRetryableHalfOpenFailure(t *testing.T) {
	s := newAICCDispatcherStoreFake()
	d := NewAICCDispatcher(s, aiccDispatcherTxFake{s}, aiccDispatcherChatFake{err: errors.New("invalid request")}, nil)
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	current := now
	d.now = func() time.Time { return current }
	for range 5 {
		d.recordOverload(now)
	}
	current = now.Add(30 * time.Second)

	require.NoError(t, d.Dispatch(context.Background(), s.task))
	assert.False(t, d.allow(current.Add(time.Second)))
	assert.True(t, d.allow(current.Add(30*time.Second)))
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
