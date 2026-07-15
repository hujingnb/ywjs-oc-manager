package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/guregu/null/v5"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

const aiccTaskLeaseDuration = 30 * time.Second

// aiccLeaseHeartbeatInterval 小于租约时长，给一次数据库抖动留下续租余量；测试可临时缩短该值。
var aiccLeaseHeartbeatInterval = 10 * time.Second

// ErrAICCLeaseLost 表示任务已不再由当前 worker 持有，后续不得写回复或累计熔断状态。
var ErrAICCLeaseLost = errors.New("aicc task lease lost")

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
	RenewAICCMessageTaskLease(context.Context, sqlc.RenewAICCMessageTaskLeaseParams) (int64, error)
	RecoverExpiredAICCMessageTaskLeases(context.Context) (int64, error)
	CreateAICCMessage(context.Context, sqlc.CreateAICCMessageParams) error
	GetAICCSessionContext(context.Context, string) (sqlc.AiccSessionContext, error)
	ListAICCContextMessages(context.Context, string) ([]sqlc.AiccMessage, error)
}

// AICCDispatcherTxRunner 保证助手消息镜像和 completed 状态不会半成功。
type AICCDispatcherTxRunner interface {
	WithAICCDispatcherTx(context.Context, func(AICCDispatcherStore) error) error
}

// AICCDispatcher 处理单个已入队客服任务；任务表的租约和会话约束仍是跨进程真相。
type AICCDispatcher struct {
	store   AICCDispatcherStore
	tx      AICCDispatcherTxRunner
	chat    AICCHermesChat
	limiter AICCConcurrencyLimiter
	circuit AICCUpstreamCircuit
	// observer 只接收固定的安全观测字段，避免 dispatcher 将访客原文写出服务边界。
	observer     AICCDispatchObserver
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

// SetObserver 注入可选的安全观测器；生产入口使用 slog，测试可替换为内存接收端。
func (d *AICCDispatcher) SetObserver(observer AICCDispatchObserver) {
	if d != nil {
		d.observer = observer
	}
}

// SetUpstreamCircuit 注入 Redis 共享熔断器；生产环境以 upstream 为隔离边界，避免副本各自失忆。
func (d *AICCDispatcher) SetUpstreamCircuit(circuit AICCUpstreamCircuit) {
	if d != nil {
		d.circuit = circuit
	}
}

// Dispatch 领取 taskID 的租约并执行；未领取到租约表示已被其他 worker 接管，不视为错误。
func (d *AICCDispatcher) Dispatch(ctx context.Context, task sqlc.AiccMessageTask) error {
	if d == nil || d.store == nil || d.chat == nil || d.tx == nil {
		return errors.New("aicc dispatcher unavailable")
	}
	if d.circuit != nil {
		allowed, err := d.circuit.Allow(ctx, "hermes")
		if err != nil || !allowed {
			d.observe(ctx, task, "circuit_open", "circuit_open")
			return err
		}
	} else if !d.allow(d.now()) {
		d.observe(ctx, task, "circuit_open", "circuit_open")
		return nil
	}
	token := newUUID()
	// 租约起止由 SQL 的 NOW(6) 计算，worker 本地时钟只用于熔断和退避，不参与所有权判定。
	claimed, err := d.store.LeaseAICCMessageTask(ctx, sqlc.LeaseAICCMessageTaskParams{ID: task.ID, LeaseToken: null.StringFrom(token)})
	if err != nil || claimed == 0 {
		if d.circuit != nil {
			_ = d.circuit.Reopen(ctx, "hermes")
		} else {
			d.releaseHalfOpenProbe()
		}
		return err
	}
	if d.limiter == nil {
		return d.finishError(ctx, task, token, ErrAICCConcurrencyLimited)
	}
	release, acquireErr := d.limiter.Acquire(ctx, task.OrgID, task.AgentID, task.SessionID)
	if acquireErr != nil {
		return d.finishError(ctx, task, token, acquireErr)
	}
	defer release()
	visitor, err := d.store.GetAICCMessageByID(ctx, task.MessageID)
	if err != nil {
		return d.finishError(ctx, task, token, err)
	}
	agent, err := d.store.GetAICCAgent(ctx, task.AgentID)
	if err != nil {
		return d.finishError(ctx, task, token, err)
	}
	conversationContext, err := BuildAICCConversationContext(ctx, d.store, task.SessionID)
	if err != nil {
		return d.finishError(ctx, task, token, err)
	}
	chatCtx, cancelChat := context.WithCancel(ctx)
	stopHeartbeat := d.startLeaseHeartbeat(chatCtx, cancelChat, task.ID, token)
	turn := AICCInboundTurn{TurnID: task.MessageID, SessionID: task.SessionID, Channel: "web_link", Text: visitor.TextContent.String, OccurredAt: d.now(), Context: conversationContext, Instruction: buildAICCRuntimePrompt(agent, ""), AppID: task.AppID}
	reply, err := d.chat.ChatAICC(chatCtx, turn)
	if err != nil {
		if heartbeatErr := stopHeartbeat(); heartbeatErr != nil {
			return heartbeatErr
		}
		return d.finishError(ctx, task, token, err)
	}
	write := func(s AICCDispatcherStore) error {
		if err := s.CreateAICCMessage(ctx, sqlc.CreateAICCMessageParams{ID: newUUID(), SessionID: task.SessionID, AgentID: task.AgentID, Direction: domain.AICCMessageDirectionAssistant, ContentType: domain.AICCMessageContentTypeText, TextContent: null.StringFrom(reply.Text), ClientMessageID: visitor.ClientMessageID, ReplyToMessageID: null.StringFrom(task.MessageID)}); err != nil {
			return err
		}
		rows, err := s.CompleteAICCMessageTask(ctx, sqlc.CompleteAICCMessageTaskParams{ID: task.ID, LeaseToken: null.StringFrom(token)})
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrAICCLeaseLost
		}
		return nil
	}
	txErr := d.tx.WithAICCDispatcherTx(ctx, write)
	heartbeatErr := stopHeartbeat()
	if heartbeatErr != nil {
		return heartbeatErr
	}
	if txErr != nil {
		if d.circuit != nil {
			_ = d.circuit.Reopen(ctx, "hermes")
		} else {
			d.reopenHalfOpenProbe(d.now())
		}
		return txErr
	}
	if d.circuit != nil {
		_ = d.circuit.RecordSuccess(ctx, "hermes")
	} else {
		d.recordSuccess()
	}
	d.observe(ctx, task, "completed", "completed")
	return nil
}

// RecoverExpiredLeases 由扫库 worker 定期调用，使宕机 worker 的任务重新可领取。
func (d *AICCDispatcher) RecoverExpiredLeases(ctx context.Context) (int64, error) {
	recovered, err := d.store.RecoverExpiredAICCMessageTaskLeases(ctx)
	if err == nil && recovered > 0 {
		d.observe(ctx, sqlc.AiccMessageTask{}, "lease_recovered", "recovered")
	}
	return recovered, err
}

func (d *AICCDispatcher) finishError(ctx context.Context, task sqlc.AiccMessageTask, token string, err error) error {
	if d.circuit != nil {
		if !errors.Is(err, ErrAICCConcurrencyLimited) && !isAICCRetryable(err) {
			_ = d.circuit.Reopen(ctx, "hermes")
		}
	} else {
		d.reopenHalfOpenProbe(d.now())
	}
	if isAICCRetryable(err) {
		// Lease SQL 会先将 attempts 加一；重试退避必须使用本轮实际失败后的计数。
		attempts := task.Attempts + 1
		rows, updateErr := d.store.RetryAICCMessageTask(ctx, sqlc.RetryAICCMessageTaskParams{ID: task.ID, LeaseToken: null.StringFrom(token), RunAfter: d.now().Add(aiccRetryDelayWithJitter(attempts, task.ID)), LastError: null.StringFrom(aiccErrorSummary(err))})
		if updateErr != nil {
			return updateErr
		}
		if rows != 1 {
			return ErrAICCLeaseLost
		}
		if d.circuit != nil && !errors.Is(err, ErrAICCConcurrencyLimited) {
			_ = d.circuit.RecordOverload(ctx, "hermes")
		} else if !errors.Is(err, ErrAICCConcurrencyLimited) {
			d.recordOverload(d.now())
		}
		if attempts >= task.MaxAttempts {
			// SQL 会将耗尽重试配额的任务原子转为 failed，观测结果必须与持久化终态一致。
			d.observe(ctx, task, "failed", aiccResultLabel(err, "failed"))
			return nil
		}
		d.observe(ctx, task, "retry", aiccResultLabel(err, "retry"))
		return nil
	}
	rows, updateErr := d.store.FailAICCMessageTask(ctx, sqlc.FailAICCMessageTaskParams{ID: task.ID, LeaseToken: null.StringFrom(token), LastError: null.StringFrom(aiccErrorSummary(err))})
	if updateErr != nil {
		return updateErr
	}
	if rows != 1 {
		return ErrAICCLeaseLost
	}
	d.observe(ctx, task, "failed", aiccResultLabel(err, "failed"))
	return nil
}

// observe 统一构造受限标签集，任何调用点都不能将访客文本或租约 token 写入观测系统。
func (d *AICCDispatcher) observe(ctx context.Context, task sqlc.AiccMessageTask, event, result string) {
	if d != nil && d.observer != nil {
		d.observer.Observe(ctx, NewAICCDispatchObservation(event, task.AppID, task.AgentID, task.OrgID, "hermes", result, 0, 0))
	}
}

// aiccResultLabel 将上游错误归并为稳定结果枚举，避免日志标签携带动态错误文本。
func aiccResultLabel(err error, prefix string) string {
	// 与 worker 共用错误分类，dispatcher 仅替换结果前缀以保留 retry/failed 生命周期语义。
	return prefix + "_" + strings.TrimPrefix(AICCSafeDispatchResult(err), "dispatch_")
}

// startLeaseHeartbeat 在 ChatAICC 执行期间使用同一 lease token 续租；续租失败会取消聊天请求，
// 防止 worker 在已失去所有权后仍将回复写回数据库。
func (d *AICCDispatcher) startLeaseHeartbeat(ctx context.Context, cancel context.CancelFunc, taskID, token string) func() error {
	ticker := time.NewTicker(aiccLeaseHeartbeatInterval)
	done := make(chan struct{})
	stopped := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		defer close(stopped)
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				rows, err := d.store.RenewAICCMessageTaskLease(ctx, sqlc.RenewAICCMessageTaskLeaseParams{ID: taskID, LeaseToken: null.StringFrom(token)})
				if err == nil && rows != 1 {
					err = ErrAICCLeaseLost
				}
				if err != nil {
					select {
					case errCh <- err:
					default:
					}
					cancel()
					return
				}
			}
		}
	}()
	return func() error {
		ticker.Stop()
		// 先取消续租请求；数据库或网络阻塞时 goroutine 才能从 ctx.Done 退出，避免 stop 死锁。
		cancel()
		close(done)
		<-stopped
		select {
		case err := <-errCh:
			return err
		default:
			return nil
		}
	}
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
	if errors.Is(err, ErrAICCConcurrencyLimited) {
		return true
	}
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
