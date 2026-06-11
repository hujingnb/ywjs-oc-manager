package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	mlog "oc-manager/internal/log"
)

// PeriodicReconciler 是一个简单的"周期触发 fn"工具，
// cmd/server 可以用它把多个 reconciler 同时挂在 errgroup 上。
type PeriodicReconciler struct {
	name     string
	interval time.Duration
	fn       func(ctx context.Context) error
}

// NewPeriodicReconciler 创建一个周期任务。
func NewPeriodicReconciler(name string, interval time.Duration, fn func(ctx context.Context) error) *PeriodicReconciler {
	return &PeriodicReconciler{name: name, interval: interval, fn: fn}
}

// Run 在 ctx 取消之前周期触发 fn。任何错误只输出到 logger，不阻断后续轮询。
func (p *PeriodicReconciler) Run(ctx context.Context, logger *slog.Logger) error {
	if p.fn == nil {
		return fmt.Errorf("reconciler %s 未配置 fn", p.name)
	}
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := p.fn(ctx); err != nil {
				logger.ErrorContext(ctx, "reconciler tick 失败",
					"name", p.name,
					mlog.Err(err),
				)
			}
		}
	}
}

// Name 返回 reconciler 名称，便于日志输出。
func (p *PeriodicReconciler) Name() string { return p.name }
