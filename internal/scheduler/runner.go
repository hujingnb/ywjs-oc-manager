package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Loop 周期性触发 Scheduler.Tick。
//
// 与 worker.Pool 的差异：
//   - scheduler 是单线程：横向扩 manager 时由 PostgreSQL 锁或单实例租约保证唯一调度；
//   - 任何 Tick 错误只走日志，不阻断后续轮询；
//   - ctx.Done 直接退出，无 goroutine 泄漏。
type Loop struct {
	scheduler *Scheduler
	interval  time.Duration
	logger    *slog.Logger
}

// NewLoop 创建 scheduler loop。interval<=0 时退化为 5 秒，与 spec §5 默认一致。
func NewLoop(scheduler *Scheduler, interval time.Duration) *Loop {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &Loop{scheduler: scheduler, interval: interval, logger: slog.Default()}
}

// SetLogger 替换结构化 logger。仅供 cmd/server 启动期调用。
func (l *Loop) SetLogger(logger *slog.Logger) {
	if logger == nil {
		return
	}
	l.logger = logger
}

// Run 周期性触发 Scheduler.Tick；ctx 取消时返回 nil。
func (l *Loop) Run(ctx context.Context) error {
	if l.scheduler == nil {
		return fmt.Errorf("scheduler loop 未配置 scheduler")
	}
	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := l.scheduler.Tick(ctx); err != nil {
				l.logger.Error("scheduler tick 错误", "error", err)
			}
		}
	}
}
