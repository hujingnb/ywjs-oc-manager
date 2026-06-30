package webpublish

import (
	"context"
	"log/slog"
	"time"

	ocredis "oc-manager/internal/redis"
)

// Loop 在后台周期调用 SiteReaper.ReapOnce，通过 Redis 分布式锁保证多副本互斥。
// 设计对齐 internal/worker/reaper/reaper.go，cmd/server 装配方式相同。
type Loop struct {
	reaper     *SiteReaper
	locker     ocredis.DistLocker
	logger     *slog.Logger
	instanceID string

	// lockTTL Redis 锁过期时间，须大于单次 ReapOnce 预期耗时；
	// 持锁实例崩溃后 lockTTL 到期，其他副本自然接管。
	lockTTL time.Duration
	// tick 两次扫描之间的间隔，默认 60s。
	tick time.Duration
}

// NewLoop 构造后台 Loop。instanceID 推荐复用 main.go 的进程 UUID，
// 使 Redis 锁 token 含进程身份，便于审计 / 排障。
func NewLoop(reaper *SiteReaper, locker ocredis.DistLocker, instanceID string, logger *slog.Logger) *Loop {
	return &Loop{
		reaper:     reaper,
		locker:     locker,
		logger:     logger,
		instanceID: instanceID,
		lockTTL:    30 * time.Second,
		tick:       60 * time.Second,
	}
}

// Start 启动后台 goroutine：进程启动时立刻跑一次，然后每 60s tick。
// ctx 取消即退出；调用方负责生命周期（如通过 context.WithCancel 控制）。
func (l *Loop) Start(ctx context.Context) {
	go func() {
		// 进程刚启动立刻跑一次，接管自己上次留下的过期站点。
		l.tickOnce(ctx)
		ticker := time.NewTicker(l.tick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				l.tickOnce(ctx)
			}
		}
	}()
}

// tickOnce 抢 Redis 锁 → ReapOnce → 释放锁。任何错误仅记日志，不中断后续 tick。
func (l *Loop) tickOnce(ctx context.Context) {
	const lockKey = "ocm:webpublish-reaper:lock"
	token := l.instanceID + ":" + nowWebPublishToken()
	got, err := l.locker.TryAcquire(ctx, lockKey, token, l.lockTTL)
	if err != nil {
		l.logger.Error("webpublish reaper 抢锁失败", "error", err)
		return
	}
	if !got {
		// 其他副本正在跑，本轮跳过。
		return
	}
	// 用 context.Background() 释放锁：父 ctx 已取消时仍要尝试释放，
	// 否则关停 / 崩溃时锁残留到 TTL，延迟其他副本接管。
	defer func() { _ = l.locker.Release(context.Background(), lockKey, token) }()
	if err := l.reaper.ReapOnce(ctx); err != nil {
		l.logger.Error("webpublish reaper 单轮扫描失败", "error", err)
	}
}

// nowWebPublishToken 返回当前 UTC 时间戳字符串，拼入 lock token 后缀，
// 避免同进程在锁过期边缘场景下复用同一 token 造成撞车。
func nowWebPublishToken() string {
	return time.Now().UTC().Format("20060102150405.000000")
}
