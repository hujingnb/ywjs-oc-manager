package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/guregu/null/v5"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

const aiccTaskLeaseDuration = 30 * time.Second

// AICCUpstreamStatusError 保留运行时上游的 HTTP 状态，供调度器区分可恢复过载与确定性失败。
type AICCUpstreamStatusError struct{ StatusCode int }

func (e *AICCUpstreamStatusError) Error() string {
	return fmt.Sprintf("aicc upstream status %d", e.StatusCode)
}

// AICCConcurrencyLimiter 为不同维度的运行时额度提供统一入口；release 必须在本轮任务结束时调用。
type AICCConcurrencyLimiter interface {
	Acquire(ctx context.Context, orgID, agentID, sessionID string) (release func(), err error)
}

// AICCDispatcherStore 是 dispatcher 读取任务上下文并更新任务状态的最小持久化接口。
type AICCDispatcherStore interface {
	GetAICCMessageByID(context.Context, string) (sqlc.AiccMessage, error)
	GetAICCAgent(context.Context, string) (sqlc.AiccAgent, error)
	LeaseAICCMessageTask(context.Context, sqlc.LeaseAICCMessageTaskParams) (int64, error)
	CompleteAICCMessageTask(context.Context, sqlc.CompleteAICCMessageTaskParams) (int64, error)
	RetryAICCMessageTask(context.Context, sqlc.RetryAICCMessageTaskParams) (int64, error)
	FailAICCMessageTask(context.Context, sqlc.FailAICCMessageTaskParams) (int64, error)
	RecoverExpiredAICCMessageTaskLeases(context.Context) (int64, error)
	CreateAICCMessage(context.Context, sqlc.CreateAICCMessageParams) error
}

// AICCDispatcherTxRunner 保证助手消息镜像和 completed 状态不会半成功。
type AICCDispatcherTxRunner interface {
	WithAICCDispatcherTx(context.Context, func(AICCDispatcherStore) error) error
}

// AICCDispatcher 处理单个已入队客服任务；任务表的租约和会话约束仍是跨进程真相。
type AICCDispatcher struct {
	store        AICCDispatcherStore
	tx           AICCDispatcherTxRunner
	chat         AICCHermesChat
	limiter      AICCConcurrencyLimiter
	now          func() time.Time
	mu           sync.Mutex
	overloads    int
	circuitUntil time.Time
	halfOpen     bool
}

// NewAICCDispatcher 创建可在 worker 中复用的单任务调度器。
func NewAICCDispatcher(store AICCDispatcherStore, tx AICCDispatcherTxRunner, chat AICCHermesChat, limiter AICCConcurrencyLimiter) *AICCDispatcher {
	return &AICCDispatcher{store: store, tx: tx, chat: chat, limiter: limiter, now: time.Now}
}

// Dispatch 领取 taskID 的租约并执行；未领取到租约表示已被其他 worker 接管，不视为错误。
func (d *AICCDispatcher) Dispatch(ctx context.Context, task sqlc.AiccMessageTask) error {
	if d == nil || d.store == nil || d.chat == nil || d.tx == nil {
		return errors.New("aicc dispatcher unavailable")
	}
	if !d.allow(d.now()) {
		return nil
	}
	token := newUUID()
	claimed, err := d.store.LeaseAICCMessageTask(ctx, sqlc.LeaseAICCMessageTaskParams{ID: task.ID, LeaseToken: null.StringFrom(token), LeaseExpiresAt: null.TimeFrom(d.now().Add(aiccTaskLeaseDuration))})
	if err != nil || claimed == 0 {
		d.releaseHalfOpenProbe()
		return err
	}
	if d.limiter != nil {
		release, acquireErr := d.limiter.Acquire(ctx, task.OrgID, task.AgentID, task.SessionID)
		if acquireErr != nil {
			return d.finishError(ctx, task, token, acquireErr)
		}
		defer release()
	}
	visitor, err := d.store.GetAICCMessageByID(ctx, task.MessageID)
	if err != nil {
		return d.finishError(ctx, task, token, err)
	}
	agent, err := d.store.GetAICCAgent(ctx, task.AgentID)
	if err != nil {
		return d.finishError(ctx, task, token, err)
	}
	reply, err := d.chat.ChatAICC(ctx, task.AppID, task.SessionID, buildAICCRuntimePrompt(agent, visitor.TextContent.String))
	if err != nil {
		return d.finishError(ctx, task, token, err)
	}
	write := func(s AICCDispatcherStore) error {
		if err := s.CreateAICCMessage(ctx, sqlc.CreateAICCMessageParams{ID: newUUID(), SessionID: task.SessionID, AgentID: task.AgentID, Direction: domain.AICCMessageDirectionAssistant, ContentType: domain.AICCMessageContentTypeText, TextContent: null.StringFrom(reply), ClientMessageID: visitor.ClientMessageID}); err != nil {
			return err
		}
		rows, err := s.CompleteAICCMessageTask(ctx, sqlc.CompleteAICCMessageTaskParams{ID: task.ID, LeaseToken: null.StringFrom(token)})
		if err != nil {
			return err
		}
		if rows != 1 {
			return errors.New("aicc task lease lost")
		}
		return nil
	}
	if err := d.tx.WithAICCDispatcherTx(ctx, write); err != nil {
		d.reopenHalfOpenProbe(d.now())
		return err
	}
	d.recordSuccess()
	return nil
}

// RecoverExpiredLeases 由扫库 worker 定期调用，使宕机 worker 的任务重新可领取。
func (d *AICCDispatcher) RecoverExpiredLeases(ctx context.Context) (int64, error) {
	return d.store.RecoverExpiredAICCMessageTaskLeases(ctx)
}

func (d *AICCDispatcher) finishError(ctx context.Context, task sqlc.AiccMessageTask, token string, err error) error {
	d.reopenHalfOpenProbe(d.now())
	if isAICCRetryable(err) {
		d.recordOverload(d.now())
		// Lease SQL 会先将 attempts 加一；重试退避必须使用本轮实际失败后的计数。
		attempts := task.Attempts + 1
		_, updateErr := d.store.RetryAICCMessageTask(ctx, sqlc.RetryAICCMessageTaskParams{ID: task.ID, LeaseToken: null.StringFrom(token), RunAfter: d.now().Add(aiccRetryDelayWithJitter(attempts, task.ID)), LastError: null.StringFrom(aiccErrorSummary(err))})
		if updateErr != nil {
			return updateErr
		}
		return nil
	}
	_, updateErr := d.store.FailAICCMessageTask(ctx, sqlc.FailAICCMessageTaskParams{ID: task.ID, LeaseToken: null.StringFrom(token), LastError: null.StringFrom(aiccErrorSummary(err))})
	return updateErr
}

// reopenHalfOpenProbe 把已领取但未成功完成的半开探测重新熔断 30 秒，
// 防止确定性失败或持久化失败留下永久 half-open 状态。
func (d *AICCDispatcher) reopenHalfOpenProbe(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.halfOpen && !d.circuitUntil.IsZero() {
		d.circuitUntil = now.Add(30 * time.Second)
		d.halfOpen = false
	}
}

func aiccRetryDelay(attempts int32) time.Duration {
	delays := []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second}
	i := int(attempts) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(delays) {
		i = len(delays) - 1
	}
	return delays[i]
}

// aiccRetryDelayWithJitter 在固定退避阶梯上增加不超过一秒的确定性抖动；
// 同一任务重试时间可复现，不同任务不会在同一秒集中回灌上游。
func aiccRetryDelayWithJitter(attempts int32, taskID string) time.Duration {
	var sum int
	for _, r := range taskID {
		sum += int(r)
	}
	return aiccRetryDelay(attempts) + time.Duration(sum%1000)*time.Millisecond
}
func aiccErrorSummary(err error) string {
	s := err.Error()
	if len(s) > 512 {
		s = s[:512]
	}
	return s
}
func isAICCRetryable(err error) bool {
	var status *AICCUpstreamStatusError
	if errors.As(err, &status) {
		return status.StatusCode == 429 || status.StatusCode == 503 || status.StatusCode == 529
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var nerr net.Error
	return errors.As(err, &nerr) && nerr.Timeout()
}
func (d *AICCDispatcher) allow(now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if now.Before(d.circuitUntil) {
		return false
	}
	if !d.circuitUntil.IsZero() {
		// 冷却期结束后只允许一条半开探测任务，探测完成前其余任务继续等待。
		if d.halfOpen {
			return false
		}
		d.halfOpen = true
	}
	return true
}
func (d *AICCDispatcher) recordOverload(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.overloads++
	if d.overloads >= 5 {
		d.circuitUntil = now.Add(30 * time.Second)
		d.halfOpen = false
	}
}
func (d *AICCDispatcher) recordSuccess() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.overloads = 0
	d.circuitUntil = time.Time{}
	d.halfOpen = false
}

// releaseHalfOpenProbe 在半开探测任务未能领取租约时释放探测权，避免空任务永久阻塞后续工作。
func (d *AICCDispatcher) releaseHalfOpenProbe() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.circuitUntil.IsZero() {
		d.halfOpen = false
	}
}
