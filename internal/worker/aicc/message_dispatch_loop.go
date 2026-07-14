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
	CountReadyAICCMessageTasksByApp(context.Context) ([]sqlc.CountReadyAICCMessageTasksByAppRow, error)
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
	// inflightByApp 只统计本进程已提交的调用；跨副本总量由指标 adapter 按 app_id 求和。
	inflightMu    sync.Mutex
	inflightByApp map[string]int64
}

// NewMessageDispatchLoop 创建一秒一次的运行循环；每轮限制领取数量，避免长任务饿死其他后台任务。
func NewMessageDispatchLoop(store messageTaskStore, queue redis.Queue, dispatcher messageTaskDispatcher, logger *slog.Logger) *MessageDispatchLoop {
	if logger == nil {
		logger = slog.Default()
	}
	return &MessageDispatchLoop{
		store: store, queue: queue, dispatcher: dispatcher, logger: logger,
		interval: time.Second, batchSize: aiccMessageDispatchBatchSize,
		slots:         make(chan struct{}, aiccMessageDispatchConcurrency),
		observer:      service.NewSlogAICCDispatchObserver(logger),
		inflightByApp: make(map[string]int64),
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
	if _, err := l.dispatcher.RecoverExpiredLeases(ctx); err != nil {
		return fmt.Errorf("回收过期 AICC 消息租约失败: %w", err)
	}
	// queue gauge 必须先读取不带 LIMIT 的分组真值；后续 ready 仅用于本轮最多 32 条的实际分派。
	queueDepthRows, err := l.store.CountReadyAICCMessageTasksByApp(ctx)
	if err != nil {
		return fmt.Errorf("统计 AICC 就绪任务队列深度失败: %w", err)
	}
	queueDepthByApp := make(map[string]int64, len(queueDepthRows))
	var queueDepth int64
	for _, row := range queueDepthRows {
		if row.AppID == "" || row.QueueDepth <= 0 {
			// 任务表 app_id 是 HPA 标签契约；异常数据或零值都不能产生空/无意义的指标序列。
			continue
		}
		queueDepthByApp[row.AppID] = row.QueueDepth
		queueDepth += row.QueueDepth
	}
	// dispatcher 是租约恢复事件的唯一所有者；循环只负责调用入口，避免同一批恢复重复计数。
	ready, err := l.store.ListReadyAICCMessageTasks(ctx, l.batchSize)
	if err != nil {
		return fmt.Errorf("扫描就绪 AICC 消息任务失败: %w", err)
	}
	byID := make(map[string]sqlc.AiccMessageTask, len(ready))
	var queueWaitMS int64
	for _, task := range ready {
		byID[task.ID] = task
		queueWait := time.Duration(0)
		if !task.CreatedAt.IsZero() {
			queueWait = time.Since(task.CreatedAt)
		}
		queueWaitMS += queueWait.Milliseconds()
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
			l.recordInflightMetrics(len(l.slots))
		}
	}
	l.recordQueueMetrics(int(queueDepth), queueWaitMS, queueDepthByApp)
	return nil
}

// tryDispatch 非阻塞地提交一个任务；返回 false 表示并发额度已满，调用方不得等待运行时调用结束。
func (l *MessageDispatchLoop) tryDispatch(ctx context.Context, task sqlc.AiccMessageTask) bool {
	select {
	case l.slots <- struct{}{}:
		l.recordInflightMetrics(len(l.slots))
		l.adjustInflightByApp(task.AppID, 1)
		l.dispatchWG.Add(1)
		go func() {
			defer func() {
				<-l.slots
				// 槽位释放必须立即回写 gauge；错误、取消和正常完成均走该 defer。
				l.recordInflightMetrics(len(l.slots))
				l.adjustInflightByApp(task.AppID, -1)
				l.dispatchWG.Done()
			}()
			if err := l.dispatcher.Dispatch(ctx, task); err != nil {
				// 单个任务失败由 dispatcher 写入重试或失败状态，不能阻塞同批其他会话。
				l.observe(ctx, service.NewAICCDispatchObservation("dispatch_error", task.AppID, task.AgentID, task.OrgID, "hermes", service.AICCSafeDispatchResult(err), 0, len(l.slots)))
			}
		}()
		return true
	default:
		l.recordInflightMetrics(len(l.slots))
		return false
	}
}

type aiccDispatchMetricRecorder interface {
	RecordQueueMetrics(depth int, queueWaitMS int64)
	RecordInflightMetrics(inflight int)
	RecordQueueMetricsByApp(depthByApp map[string]int64)
	RecordInflightMetricsByApp(inflightByApp map[string]int64)
}

func (l *MessageDispatchLoop) recordQueueMetrics(depth int, queueWaitMS int64, depthByApp map[string]int64) {
	if recorder, ok := l.observer.(aiccDispatchMetricRecorder); ok {
		recorder.RecordQueueMetrics(depth, queueWaitMS)
		recorder.RecordQueueMetricsByApp(depthByApp)
	}
}

// adjustInflightByApp 在任务提交和退出时维护本进程 app 级 in-flight 快照。
func (l *MessageDispatchLoop) adjustInflightByApp(appID string, delta int64) {
	if l == nil || appID == "" {
		return
	}
	l.inflightMu.Lock()
	l.inflightByApp[appID] += delta
	if l.inflightByApp[appID] <= 0 {
		delete(l.inflightByApp, appID)
	}
	snapshot := make(map[string]int64, len(l.inflightByApp))
	for id, value := range l.inflightByApp {
		snapshot[id] = value
	}
	l.inflightMu.Unlock()
	if recorder, ok := l.observer.(aiccDispatchMetricRecorder); ok {
		recorder.RecordInflightMetricsByApp(snapshot)
	}
}

func (l *MessageDispatchLoop) recordInflightMetrics(inflight int) {
	if recorder, ok := l.observer.(aiccDispatchMetricRecorder); ok {
		recorder.RecordInflightMetrics(inflight)
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
		// Wait 返回时已无已提交调用，显式归零可消除并发 defer 交错留下的陈旧 gauge。
		l.recordInflightMetrics(0)
	}
}
