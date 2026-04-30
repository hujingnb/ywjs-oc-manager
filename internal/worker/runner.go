package worker

import (
	"context"
	"fmt"
	"log"
	"time"

	"golang.org/x/sync/errgroup"
)

// Pool 在固定 concurrency 下并行轮询 Worker.Tick。
//
// 关键约束：
//   - panic 由 safeRecoverTick 拦截，单次 panic 不会拖死整个 pool；
//   - ctx.Done 收到后停止 ticker，所有 goroutine 平滑退出；
//   - errgroup.Wait 总是返回 nil 除非 ctx 出错；Tick 内部错误由 cfg.OnError 上报。
type Pool struct {
	worker      *Worker
	concurrency int
	interval    time.Duration
	logger      *log.Logger
}

// NewPool 创建 worker pool。concurrency<=0 时退化为 1，interval<=0 时退化为 200ms。
// 默认 logger 走 stdlib log 包，调用方可通过 SetLogger 注入自定义日志。
func NewPool(worker *Worker, concurrency int, interval time.Duration) *Pool {
	if concurrency <= 0 {
		concurrency = 1
	}
	if interval <= 0 {
		interval = 200 * time.Millisecond
	}
	return &Pool{worker: worker, concurrency: concurrency, interval: interval, logger: log.Default()}
}

// SetLogger 替换 pool 的日志器，主要供测试或自定义日志格式使用。
func (p *Pool) SetLogger(logger *log.Logger) {
	if logger == nil {
		return
	}
	p.logger = logger
}

// Run 在 concurrency 个 goroutine 中并行轮询 Tick，直到 ctx 取消后返回 nil。
// 所有 goroutine 的运行错误都通过 logger 输出，不冒泡阻断调用方。
func (p *Pool) Run(ctx context.Context) error {
	if p.worker == nil {
		return fmt.Errorf("worker pool 未配置 worker")
	}
	eg, gctx := errgroup.WithContext(ctx)
	for i := 0; i < p.concurrency; i++ {
		i := i
		eg.Go(func() error {
			ticker := time.NewTicker(p.interval)
			defer ticker.Stop()
			for {
				select {
				case <-gctx.Done():
					return nil
				case <-ticker.C:
					if err := safeRecoverTick(gctx, p.worker); err != nil {
						p.logger.Printf("worker[%d] tick 错误: %v", i, err)
					}
				}
			}
		})
	}
	return eg.Wait()
}

// safeRecoverTick 调用 worker.Tick 并拦截 panic，把 panic 转换成 error。
// 这样单次 panic 只影响当次轮询，不会让 goroutine 退出。
func safeRecoverTick(ctx context.Context, w *Worker) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("worker panic: %v", r)
		}
	}()
	return w.Tick(ctx)
}
