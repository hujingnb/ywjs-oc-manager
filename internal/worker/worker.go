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
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
	"oc-manager/internal/worker/handlers"
)

// JobStore 抽象 worker 需要的数据访问能力。
// 仅暴露当前实际使用的方法，便于在测试中使用内存桩。
type JobStore interface {
	GetJob(ctx context.Context, id pgtype.UUID) (sqlc.Job, error)
	MarkJobRunning(ctx context.Context, arg sqlc.MarkJobRunningParams) (sqlc.Job, error)
	MarkJobSucceeded(ctx context.Context, id pgtype.UUID) (sqlc.Job, error)
	MarkJobFailed(ctx context.Context, arg sqlc.MarkJobFailedParams) (sqlc.Job, error)
	RetryJob(ctx context.Context, arg sqlc.RetryJobParams) (sqlc.Job, error)
}

// Queue 抽象 worker 信号源。与 internal/redis.Queue 保持一致以便复用实现。
type Queue interface {
	Reserve(ctx context.Context, limit int) ([]string, error)
}

// Config 描述 worker 的运行参数。
type Config struct {
	WorkerID      string
	BatchSize     int
	BackoffBase   time.Duration
	BackoffFactor float64
	BackoffMax    time.Duration
	OnError       func(jobID string, err error)
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
	jobUUID, err := parseUUID(id)
	if err != nil {
		return fmt.Errorf("非法 job id %q: %w", id, err)
	}
	job, err := w.store.GetJob(ctx, jobUUID)
	if err != nil {
		return fmt.Errorf("查询 job 失败: %w", err)
	}
	if job.Status != domain.JobStatusPending {
		// 来自队列的 jobID 可能已经被其他 worker 处理；幂等地跳过。
		return nil
	}
	running, err := w.store.MarkJobRunning(ctx, sqlc.MarkJobRunningParams{
		ID:       job.ID,
		LockedBy: pgtype.Text{String: w.cfg.WorkerID, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("锁定 job 失败: %w", err)
	}
	handler, err := w.registry.Lookup(running.Type)
	if err != nil {
		_, finalErr := w.store.MarkJobFailed(ctx, sqlc.MarkJobFailedParams{
			ID:        running.ID,
			LastError: pgtype.Text{String: err.Error(), Valid: true},
		})
		if finalErr != nil {
			return fmt.Errorf("标记 job 失败失败: %w", finalErr)
		}
		return nil
	}
	if err := handler(ctx, running); err != nil {
		return w.handleHandlerError(ctx, running, err)
	}
	if _, err := w.store.MarkJobSucceeded(ctx, running.ID); err != nil {
		return fmt.Errorf("标记 job 成功失败: %w", err)
	}
	return nil
}

func (w *Worker) handleHandlerError(ctx context.Context, job sqlc.Job, handlerErr error) error {
	if job.Attempts >= job.MaxAttempts {
		if _, err := w.store.MarkJobFailed(ctx, sqlc.MarkJobFailedParams{
			ID:        job.ID,
			LastError: pgtype.Text{String: handlerErr.Error(), Valid: true},
		}); err != nil {
			return fmt.Errorf("标记 job 失败失败: %w", err)
		}
		return nil
	}
	delay := w.backoff(int(job.Attempts))
	runAfter := w.now().Add(delay)
	if _, err := w.store.RetryJob(ctx, sqlc.RetryJobParams{
		ID:        job.ID,
		RunAfter:  pgtype.Timestamptz{Time: runAfter, Valid: true},
		LastError: pgtype.Text{String: handlerErr.Error(), Valid: true},
	}); err != nil {
		return fmt.Errorf("重试 job 失败: %w", err)
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

func parseUUID(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		return pgtype.UUID{}, err
	}
	return id, nil
}
