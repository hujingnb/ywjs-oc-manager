package aicc

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"oc-manager/internal/redis"
	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

const aiccMessageDispatchBatchSize int32 = 32

const aiccMessageDispatchConcurrency = 4

// messageTaskStore 是公开消息运行循环所需的最小 MySQL 查询集合。
// MySQL 保存任务事实与租约，Redis 只提供低延迟的唤醒信号。
type messageTaskStore interface {
	ListReadyAICCMessageTasks(context.Context, int32) ([]sqlc.AiccMessageTask, error)
}

// messageTaskDispatcher 抽象单条任务执行，便于循环独立测试调度顺序和故障降级。
type messageTaskDispatcher interface {
	Dispatch(context.Context, sqlc.AiccMessageTask) error
	RecoverExpiredLeases(context.Context) (int64, error)
}

// MessageDispatchLoop 周期扫描并执行 AICC 公开消息的异步任务。
type MessageDispatchLoop struct {
	store      messageTaskStore
	queue      redis.Queue
	dispatcher messageTaskDispatcher
	logger     *slog.Logger
	// observer 与 dispatcher 共用同一安全事件出口，确保循环级排队/恢复事件不绕过脱敏规则。
	observer  service.AICCDispatchObserver
	interval  time.Duration
	batchSize int32
	// slots 控制同时调用运行时的数量，避免慢请求无限堆积或阻塞扫描循环。
	slots chan struct{}
	// dispatchWG 让 shutdown 等待已启动任务退出，避免主进程提前关闭依赖连接。
	dispatchWG sync.WaitGroup
}

// NewMessageDispatchLoop 创建一秒一次的运行循环；每轮限制领取数量，避免长任务饿死其他后台任务。
func NewMessageDispatchLoop(store messageTaskStore, queue redis.Queue, dispatcher messageTaskDispatcher, logger *slog.Logger) *MessageDispatchLoop {
	if logger == nil {
		logger = slog.Default()
	}
	return &MessageDispatchLoop{
		store: store, queue: queue, dispatcher: dispatcher, logger: logger,
		interval: time.Second, batchSize: aiccMessageDispatchBatchSize,
		slots:    make(chan struct{}, aiccMessageDispatchConcurrency),
		observer: service.NewSlogAICCDispatchObserver(logger),
	}
}

// SetObserver 注入与 dispatcher 共享的安全观测器；用于保证同一任务生命周期在同一出口记录。
func (l *MessageDispatchLoop) SetObserver(observer service.AICCDispatchObserver) {
	if l != nil && observer != nil {
		l.observer = observer
	}
}

// Run 在进程生命周期内周期触发 Tick；错误由日志记录，后续轮次继续以 MySQL 扫描恢复。
func (l *MessageDispatchLoop) Run(ctx context.Context) error {
	if l == nil || l.store == nil || l.queue == nil || l.dispatcher == nil {
		return fmt.Errorf("AICC 消息运行循环未配置")
	}
	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			l.Wait()
			return nil
		case <-ticker.C:
			if err := l.Tick(ctx); err != nil {
				l.logger.ErrorContext(ctx, "AICC 消息运行循环执行失败", "error", err)
			}
		}
	}
}

// Tick 回收过期租约、扫 MySQL 就绪任务并补发 Redis 信号，随后领取一个有界批次执行。
// Redis 不可用时立即返回错误；任务仍留在 MySQL，下一轮扫描会再次尝试入队。
func (l *MessageDispatchLoop) Tick(ctx context.Context) error {
	if l == nil || l.store == nil || l.queue == nil || l.dispatcher == nil {
		return fmt.Errorf("AICC 消息运行循环未配置")
	}
	// 必须经 dispatcher 的恢复入口执行，避免循环绕过其观测与未来的恢复策略。
	recovered, err := l.dispatcher.RecoverExpiredLeases(ctx)
	if err != nil {
		return fmt.Errorf("回收过期 AICC 消息租约失败: %w", err)
	}
	if recovered > 0 {
		// 租约回收为聚合事件，不带任务或会话标识；MySQL 状态仍是重启恢复的唯一事实来源。
		l.observe(ctx, service.AICCDispatchObservation{Event: "lease_recovered", Upstream: "hermes", Result: "recovered"})
	}
	ready, err := l.store.ListReadyAICCMessageTasks(ctx, l.batchSize)
	if err != nil {
		return fmt.Errorf("扫描就绪 AICC 消息任务失败: %w", err)
	}
	byID := make(map[string]sqlc.AiccMessageTask, len(ready))
	for _, task := range ready {
		byID[task.ID] = task
		queueWait := time.Duration(0)
		if !task.CreatedAt.IsZero() {
			queueWait = time.Since(task.CreatedAt)
		}
		// queued 事件只记录稳定任务归属和等待时长，不记录访客输入、session 或 Redis 信号 ID。
		l.observe(ctx, service.AICCDispatchObservation{Event: "queued", AgentID: task.AgentID, OrgID: task.OrgID, Upstream: "hermes", Result: "queued", QueueWaitMS: queueWait.Milliseconds()})
		if err := l.queue.Enqueue(ctx, task.ID); err != nil {
			return fmt.Errorf("写入 AICC 消息 Redis 信号失败: %w", err)
		}
	}
	ids, err := l.queue.Reserve(ctx, int(l.batchSize))
	if err != nil {
		return fmt.Errorf("领取 AICC 消息 Redis 信号失败: %w", err)
	}
	for _, id := range ids {
		task, ok := byID[id]
		if !ok {
			// 信号可能来自上一轮；本轮扫描未见到它时说明它已不再可执行，安全跳过。
			continue
		}
		if !l.tryDispatch(ctx, task) {
			// 有界并发已满时不阻塞扫描；该任务仍是 MySQL 就绪任务，下轮会重新入队。
			l.observe(ctx, service.AICCDispatchObservation{Event: "queued", AgentID: task.AgentID, OrgID: task.OrgID, Upstream: "hermes", Result: "concurrency_limited", Inflight: len(l.slots)})
		}
	}
	return nil
}

// tryDispatch 非阻塞地提交一个任务；返回 false 表示并发额度已满，调用方不得等待运行时调用结束。
func (l *MessageDispatchLoop) tryDispatch(ctx context.Context, task sqlc.AiccMessageTask) bool {
	select {
	case l.slots <- struct{}{}:
		l.dispatchWG.Add(1)
		go func() {
			defer func() {
				<-l.slots
				l.dispatchWG.Done()
			}()
			if err := l.dispatcher.Dispatch(ctx, task); err != nil {
				// 单个任务失败由 dispatcher 写入重试或失败状态，不能阻塞同批其他会话。
				l.observe(ctx, service.AICCDispatchObservation{Event: "dispatch_error", AgentID: task.AgentID, OrgID: task.OrgID, Upstream: "hermes", Result: service.AICCSafeDispatchResult(err), Inflight: len(l.slots)})
			}
		}()
		return true
	default:
		return false
	}
}

// observe 统一把循环事件交给安全观测器；任何任务文本、会话 ID、租约 token 和原始错误均不得传入。
func (l *MessageDispatchLoop) observe(ctx context.Context, event service.AICCDispatchObservation) {
	if l != nil && l.observer != nil {
		l.observer.Observe(ctx, event)
	}
}

// Wait 等待当前已提交的运行时调用退出；Run 在收到 shutdown 信号后调用它。
func (l *MessageDispatchLoop) Wait() {
	if l != nil {
		l.dispatchWG.Wait()
	}
}
