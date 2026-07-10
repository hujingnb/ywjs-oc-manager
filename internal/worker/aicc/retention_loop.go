// Package aicc 放置 AICC 后台维护任务。
package aicc

import (
	"context"
	"log/slog"
	"time"

	ocredis "oc-manager/internal/redis"
	"oc-manager/internal/service"
)

// RetentionLoop 周期执行 AICC 会话保留期清理，并用 Redis 锁保证多副本互斥。
type RetentionLoop struct {
	// cleaner 是实际删除过期会话和对象存储图片的业务服务。
	cleaner *service.AICCRetentionService
	// locker 是跨 manager 副本共享的分布式锁。
	locker ocredis.DistLocker
	// logger 记录单轮执行失败，后台任务错误不影响主进程服务请求。
	logger *slog.Logger
	// instanceID 拼入锁 token，方便排查当前持锁副本。
	instanceID string
	// lockTTL 必须大于单轮预期耗时，持锁副本异常退出后由 TTL 自动释放。
	lockTTL time.Duration
	// tick 是两次清理扫描之间的间隔。
	tick time.Duration
	// limit 是单轮最多清理的会话数，避免一次扫描占用过久。
	limit int32
}

// NewRetentionLoop 创建 AICC 保留期后台清理 loop。
func NewRetentionLoop(cleaner *service.AICCRetentionService, locker ocredis.DistLocker, instanceID string, logger *slog.Logger) *RetentionLoop {
	return &RetentionLoop{
		cleaner:    cleaner,
		locker:     locker,
		logger:     logger,
		instanceID: instanceID,
		lockTTL:    30 * time.Second,
		tick:       60 * time.Second,
		limit:      100,
	}
}

// Start 启动后台 goroutine；进程启动时先清理一次，随后按 tick 周期执行。
func (l *RetentionLoop) Start(ctx context.Context) {
	go func() {
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

func (l *RetentionLoop) tickOnce(ctx context.Context) {
	const lockKey = "ocm:aicc-retention:lock"
	token := l.instanceID + ":" + time.Now().UTC().Format("20060102150405.000000")
	got, err := l.locker.TryAcquire(ctx, lockKey, token, l.lockTTL)
	if err != nil {
		l.logger.Error("AICC 保留期清理抢锁失败", "error", err)
		return
	}
	if !got {
		return
	}
	defer func() { _ = l.locker.Release(context.Background(), lockKey, token) }()
	deleted, err := l.cleaner.CleanupExpiredSessions(ctx, l.limit)
	if err != nil {
		l.logger.Error("AICC 保留期清理失败", "error", err)
		return
	}
	if deleted > 0 {
		l.logger.Info("AICC 保留期清理完成", "deleted_sessions", deleted)
	}
}
