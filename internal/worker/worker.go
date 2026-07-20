// Package worker 负责从队列与 PostgreSQL 中拉取 job、执行 handler、并写回执行结果。
//
// 设计原则：
//   - PostgreSQL 是 job 事实来源，状态机仅从 jobs 表上读写；
//   - Redis（或 MemoryQueue）作为快速信号通道，丢失也只是退化为按 run_after 扫描；
//   - handler 的失败按指数退避重试，达到 max_attempts 后写入 failed 终态；
//   - 所有外部副作用都需要 handler 自行实现幂等，worker 不做事务级回滚。
package worker

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	null "github.com/guregu/null/v5"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
	"oc-manager/internal/worker/handlers"
)

// JobStore 抽象 worker 需要的数据访问能力。
// 仅暴露当前实际使用的方法，便于在测试中使用内存桩。
type JobStore interface {
	GetJob(ctx context.Context, id string) (sqlc.Job, error)
	MarkJobRunning(ctx context.Context, arg sqlc.MarkJobRunningParams) (int64, error)
	MarkJobSucceeded(ctx context.Context, arg sqlc.MarkJobSucceededParams) (int64, error)
	MarkJobFailed(ctx context.Context, arg sqlc.MarkJobFailedParams) (int64, error)
	RetryJob(ctx context.Context, arg sqlc.RetryJobParams) (int64, error)
	RenewJobLease(ctx context.Context, arg sqlc.RenewJobLeaseParams) (int64, error)
	// DeferJob 无损释放因业务互斥暂不可执行的任务，并抵消本次领取增加的 attempts。
	DeferJob(ctx context.Context, arg sqlc.DeferJobParams) (int64, error)
}

// Queue 抽象 worker 信号源。与 internal/redis.Queue 保持一致以便复用实现。
type Queue interface {
	Reserve(ctx context.Context, limit int) ([]string, error)
}

// Config 描述 worker 的运行参数。
type Config struct {
	// WorkerID 写入 jobs.locked_by，便于排查哪一个 worker 实例抢到任务。
	WorkerID string
	// BatchSize 限制单次 Tick 从队列预定的 jobID 数量，避免单个 worker 长时间占住调度循环。
	BatchSize int
	// BackoffBase 是第一次 handler 失败后的基础重试间隔。
	BackoffBase time.Duration
	// BackoffFactor 控制后续失败的指数退避倍率，<=1 时使用默认倍率 2。
	BackoffFactor float64
	// BackoffMax 截断指数退避结果，避免故障任务无限拉长到不可观测。
	BackoffMax time.Duration
	// OnError 接收单个 job 的处理错误；Tick 会继续处理同批次其他 job。
	OnError func(jobID string, err error)
	// LeaseRenewInterval 是 handler 执行期间刷新任务 lease 的周期；默认一分钟，显著小于 recovery 的五分钟阈值。
	LeaseRenewInterval time.Duration
}

// Worker 持有 store、queue 和 handler registry，并暴露单次 Tick 的处理入口。
// 真实 server 通常会以固定间隔轮询 Tick，让 worker 可控且易测。
type Worker struct {
	store    JobStore
	queue    Queue
	registry *handlers.Registry
	cfg      Config
	now      func() time.Time
}

// New 创建 worker 实例，未提供的参数使用合理默认值。
func New(store JobStore, queue Queue, registry *handlers.Registry, cfg Config) *Worker {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 8
	}
	if cfg.BackoffBase <= 0 {
		cfg.BackoffBase = 5 * time.Second
	}
	if cfg.BackoffFactor <= 1 {
		cfg.BackoffFactor = 2
	}
	if cfg.BackoffMax <= 0 {
		cfg.BackoffMax = 5 * time.Minute
	}
	if cfg.WorkerID == "" {
		cfg.WorkerID = "worker"
	}
	if cfg.LeaseRenewInterval <= 0 {
		cfg.LeaseRenewInterval = time.Minute
	}
	return &Worker{store: store, queue: queue, registry: registry, cfg: cfg, now: time.Now}
}

// Tick 执行一轮处理：从队列预定 BatchSize 个 jobID，对每一个执行处理流程。
// 当前实现是顺序执行；并发由调用方通过启动多个 worker 协程实现。
func (w *Worker) Tick(ctx context.Context) error {
	if w.queue == nil || w.store == nil || w.registry == nil {
		return errors.New("worker 未配置 queue/store/registry")
	}
	ids, err := w.queue.Reserve(ctx, w.cfg.BatchSize)
	if err != nil {
		return fmt.Errorf("从队列预定 job 失败: %w", err)
	}
	for _, id := range ids {
		if err := w.processJobID(ctx, id); err != nil && w.cfg.OnError != nil {
			w.cfg.OnError(id, err)
		}
	}
	return nil
}

func (w *Worker) processJobID(ctx context.Context, id string) error {
	// 队列只保存字符串 jobID，真正状态仍以数据库为准；id 已经是 string 可直接使用。
	job, err := w.store.GetJob(ctx, id)
	if err != nil {
		return fmt.Errorf("查询 job 失败: %w", err)
	}
	if job.Status != domain.JobStatusPending {
		// 来自队列的 jobID 可能已经被其他 worker 处理；幂等地跳过。
		return nil
	}
	// MarkJobRunning 使用 status=pending 的条件更新和随机 lease token，读写之间被其他 worker 抢先领取时安全跳过。
	leaseToken := null.StringFrom(uuid.NewString())
	rows, err := w.store.MarkJobRunning(ctx, sqlc.MarkJobRunningParams{
		ID:         job.ID,
		LockedBy:   null.StringFrom(w.cfg.WorkerID),
		LeaseToken: leaseToken,
	})
	if err != nil {
		return fmt.Errorf("锁定 job 失败: %w", err)
	}
	if rows != 1 {
		// 其他 worker 可能已在本次读写之间领取任务；队列重复信号可安全忽略。
		return nil
	}
	// MarkJobRunning 在 DB 内执行 attempts+1，故这里在内存中同步自增，
	// 让后续 handler 与 handleHandlerError/backoff 看到与原 PG RETURNING 行一致的尝试次数，
	// 否则 max_attempts 判定会比应有次数少一次。
	job.Attempts++
	job.LockedBy = null.StringFrom(w.cfg.WorkerID)
	job.LeaseToken = leaseToken
	// 续租失败或 token 被接管时取消 handler context，尽早停止旧 owner 的外部副作用；
	// 即便某个下游调用不支持取消，后续 CAS 也会拒绝它覆盖新 owner 的任务状态。
	handlerCtx, cancelHandler := context.WithCancel(ctx)
	defer cancelHandler()
	stopRenew, leaseLost := w.startLeaseRenewer(handlerCtx, job, cancelHandler)
	defer stopRenew()
	handler, err := w.registry.Lookup(job.Type)
	if err != nil {
		// 未注册类型无法通过重试自愈，直接进入 failed 终态，避免 scheduler 反复重新入队。
		finalRows, finalErr := w.store.MarkJobFailed(ctx, sqlc.MarkJobFailedParams{
			ID: job.ID, LastError: null.StringFrom(err.Error()), LockedBy: job.LockedBy, LeaseToken: job.LeaseToken,
		})
		if finalErr != nil {
			return fmt.Errorf("标记 job 失败失败: %w", finalErr)
		}
		if finalRows != 1 {
			return ErrStaleJobOwner
		}
		return nil
	}
	if err := handler(handlerCtx, job); err != nil {
		if leaseLost.Load() {
			return ErrStaleJobOwner
		}
		var deferred *handlers.DeferredJobError
		if errors.As(err, &deferred) {
			delay := deferred.Delay
			if delay <= 0 {
				delay = time.Second
			}
			rows, deferErr := w.store.DeferJob(ctx, sqlc.DeferJobParams{ID: job.ID, RunAfter: w.now().Add(delay), LockedBy: job.LockedBy, LeaseToken: job.LeaseToken})
			if deferErr != nil {
				return fmt.Errorf("延后 job 失败: %w", deferErr)
			}
			if rows != 1 {
				return ErrStaleJobOwner
			}
			return nil
		}
		return w.handleHandlerError(ctx, job, err)
	}
	if leaseLost.Load() {
		return ErrStaleJobOwner
	}
	// 成功前回调失败走现有 retry，防止后继调度错误发生在 succeeded 后而永久丢失。
	if beforeSuccess := w.registry.LookupBeforeSuccess(job.Type); beforeSuccess != nil {
		if err := beforeSuccess(handlerCtx, job); err != nil {
			return w.handleHandlerError(ctx, job, fmt.Errorf("job 成功前回调失败: %w", err))
		}
	}
	rows, err = w.store.MarkJobSucceeded(ctx, sqlc.MarkJobSucceededParams{ID: job.ID, LockedBy: job.LockedBy, LeaseToken: job.LeaseToken})
	if err != nil {
		return fmt.Errorf("标记 job 成功失败: %w", err)
	}
	if rows != 1 {
		return ErrStaleJobOwner
	}
	return nil
}

// handleHandlerError 根据 attempts/max_attempts 决定进入 failed 终态或安排下一次重试。
// handlerErr 会被持久化到 last_error，供后台任务列表和审计排障展示。
func (w *Worker) handleHandlerError(ctx context.Context, job sqlc.Job, handlerErr error) error {
	if job.Attempts >= job.MaxAttempts {
		rows, err := w.store.MarkJobFailed(ctx, sqlc.MarkJobFailedParams{
			ID: job.ID, LastError: null.StringFrom(handlerErr.Error()), LockedBy: job.LockedBy, LeaseToken: job.LeaseToken,
		})
		if err != nil {
			return fmt.Errorf("标记 job 失败失败: %w", err)
		}
		if rows != 1 {
			return ErrStaleJobOwner
		}
		// 终态失败后补偿不能让原任务重新循环；例如提示词 successor 创建失败耗尽重试时，
		// coordinator 可在旧任务不再 active 后安全创建持久后继任务。
		if afterFailure := w.registry.LookupAfterFailure(job.Type); afterFailure != nil {
			if err := afterFailure(ctx, job); err != nil {
				return fmt.Errorf("job 终态失败补偿失败: %w", err)
			}
		}
		return nil
	}
	delay := w.backoff(int(job.Attempts))
	runAfter := w.now().Add(delay)
	rows, err := w.store.RetryJob(ctx, sqlc.RetryJobParams{
		ID: job.ID, RunAfter: runAfter, LastError: null.StringFrom(handlerErr.Error()), LockedBy: job.LockedBy, LeaseToken: job.LeaseToken,
	})
	if err != nil {
		return fmt.Errorf("重试 job 失败: %w", err)
	}
	if rows != 1 {
		return ErrStaleJobOwner
	}
	return nil
}

// backoff 返回第 attempt 次失败后的下次重试间隔。
// 使用 base * factor^(attempt-1)，并以 BackoffMax 截断，避免成倍放大。
func (w *Worker) backoff(attempt int) time.Duration {
	if attempt <= 1 {
		return w.cfg.BackoffBase
	}
	scaled := float64(w.cfg.BackoffBase) * math.Pow(w.cfg.BackoffFactor, float64(attempt-1))
	if scaled <= 0 || scaled > float64(w.cfg.BackoffMax) {
		return w.cfg.BackoffMax
	}
	return time.Duration(scaled)
}

// SetClock 替换 worker 内部时钟，仅供测试使用。
func (w *Worker) SetClock(now func() time.Time) { w.now = now }

// ErrStaleJobOwner 表示任务 lease 已被 recovery 回收并由其他 worker 接管；旧 owner 不得再覆盖新状态。
var ErrStaleJobOwner = errors.New("job lease 已失效，当前 worker 不再拥有任务")

// startLeaseRenewer 在 handler 执行期间刷新 locked_at。续租失败会取消 handler context，
// 让支持 context 的外部调用尽快停止；后续 CAS 状态写入继续保护不支持取消的旧 owner 不覆盖新状态。
func (w *Worker) startLeaseRenewer(ctx context.Context, job sqlc.Job, cancelHandler context.CancelFunc) (func(), *atomic.Bool) {
	done := make(chan struct{})
	leaseLost := &atomic.Bool{}
	go func() {
		ticker := time.NewTicker(w.cfg.LeaseRenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				rows, err := w.store.RenewJobLease(ctx, sqlc.RenewJobLeaseParams{ID: job.ID, LockedBy: job.LockedBy, LeaseToken: job.LeaseToken})
				if err != nil || rows != 1 {
					leaseLost.Store(true)
					cancelHandler()
					return
				}
			}
		}
	}()
	return func() { close(done) }, leaseLost
}
