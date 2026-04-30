// Package scheduler 周期性扫描 jobs 表，把满足 run_after <= now 的 pending job 推回 Redis 队列。
//
// 调度目标：
//   - 当 worker 因为重启或崩溃导致 Redis 信号丢失时，scheduler 自动从 PostgreSQL 重新入队，
//     避免 job 永久滞留；
//   - scheduler 只调用 ListReadyJobs 这种只读查询，不直接改写 jobs 表，状态转移仍由 worker 完成；
//   - 实现保持单线程；如果 manager 横向扩展，需借助数据库锁或租约保证唯一调度，但本轮先不引入。
package scheduler

import (
	"context"
	"errors"
	"fmt"

	"oc-manager/internal/store/sqlc"
)

// JobStore 抽象 scheduler 需要的查询能力。
type JobStore interface {
	ListReadyJobs(ctx context.Context, limit int32) ([]sqlc.Job, error)
}

// Queue 抽象信号队列能力。
type Queue interface {
	Enqueue(ctx context.Context, jobID string) error
}

// Config 描述 scheduler 行为参数。
type Config struct {
	BatchSize int32
}

// Scheduler 周期性把数据库中可执行的 job 重新推入信号队列。
type Scheduler struct {
	store JobStore
	queue Queue
	cfg   Config
}

// New 创建 scheduler。
func New(store JobStore, queue Queue, cfg Config) *Scheduler {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	return &Scheduler{store: store, queue: queue, cfg: cfg}
}

// Tick 单次执行扫描+入队。
// 调用方负责按固定间隔调用，建议 5~10 秒，避免压垮数据库。
func (s *Scheduler) Tick(ctx context.Context) error {
	if s == nil || s.store == nil || s.queue == nil {
		return errors.New("scheduler 未配置 store 或 queue")
	}
	jobs, err := s.store.ListReadyJobs(ctx, s.cfg.BatchSize)
	if err != nil {
		return fmt.Errorf("列出待执行 job 失败: %w", err)
	}
	for _, job := range jobs {
		id := uuidString(job)
		if err := s.queue.Enqueue(ctx, id); err != nil {
			return fmt.Errorf("将 job %s 入队失败: %w", id, err)
		}
	}
	return nil
}

func uuidString(job sqlc.Job) string {
	return formatUUIDBytes(job.ID.Bytes[:])
}

func formatUUIDBytes(value []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, b := range value {
		out = append(out, digits[b>>4], digits[b&0x0f])
		if i == 3 || i == 5 || i == 7 || i == 9 {
			out = append(out, '-')
		}
	}
	return string(out)
}
