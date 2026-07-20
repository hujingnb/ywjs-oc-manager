package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/guregu/null/v5"

	ocredis "oc-manager/internal/redis"
)

// JobRecoveryStore 是后台任务锁回收所需的最小数据库能力。
// *sqlc.Queries 直接满足该接口，查询在单条 UPDATE 中完成条件筛选与状态回退。
type JobRecoveryStore interface {
	RequeueExpiredRunningJobs(ctx context.Context, lockedBefore null.Time) (int64, error)
}

// JobRecoveryConfig 描述遗留 running 任务的扫描周期与安全租约。
type JobRecoveryConfig struct {
	// Lease 是任务持锁超过多久后才视为 worker 已失联。默认五分钟，给正常外部调用保留足够窗口，
	// 同时避免进程重启留下的任务长时间阻塞；任务没有心跳字段，不能使用更短阈值。
	Lease time.Duration
	// Interval 是两次扫描间隔；启动后会立刻额外执行一次，不需等待该周期。
	Interval time.Duration
}

// JobRecovery 周期性回收异常退出进程遗留的 running 锁。
// 多副本通过 Redis 锁互斥，避免多个 manager 同时写同一批 jobs；SQL 条件更新仍是最终并发保护。
type JobRecovery struct {
	store      JobRecoveryStore
	locker     ocredis.DistLocker
	instanceID string
	lease      time.Duration
	interval   time.Duration
	lockTTL    time.Duration
	logger     *slog.Logger
	now        func() time.Time
}

// NewJobRecovery 创建任务锁回收器。
func NewJobRecovery(store JobRecoveryStore, locker ocredis.DistLocker, instanceID string, cfg JobRecoveryConfig) *JobRecovery {
	if cfg.Lease <= 0 {
		cfg.Lease = 5 * time.Minute
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Minute
	}
	return &JobRecovery{
		store:      store,
		locker:     locker,
		instanceID: instanceID,
		lease:      cfg.Lease,
		interval:   cfg.Interval,
		lockTTL:    30 * time.Second,
		logger:     slog.Default(),
		now:        time.Now,
	}
}

// SetLogger 注入 server 统一的结构化日志。
func (r *JobRecovery) SetLogger(logger *slog.Logger) {
	if logger != nil {
		r.logger = logger
	}
}

// SetClock 替换当前时间来源，仅供测试稳定断言租约阈值。
func (r *JobRecovery) SetClock(now func() time.Time) {
	if now != nil {
		r.now = now
	}
}

// Start 在启动时立即扫描一次，之后按周期继续扫描；错误只记录日志，不能阻断服务。
func (r *JobRecovery) Start(ctx context.Context) {
	go func() {
		r.tickOnce(ctx)
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.tickOnce(ctx)
			}
		}
	}()
}

// Tick 执行一次受分布式锁保护的回收，返回本轮重新入队的任务数。
func (r *JobRecovery) Tick(ctx context.Context) (int64, error) {
	if r.store == nil || r.locker == nil {
		return 0, fmt.Errorf("job recovery 未配置 store 或 locker")
	}
	token := r.instanceID + ":" + r.now().UTC().Format("20060102150405.000000")
	got, err := r.locker.TryAcquire(ctx, "ocm:jobs:recovery:lock", token, r.lockTTL)
	if err != nil {
		return 0, fmt.Errorf("job recovery 抢锁: %w", err)
	}
	if !got {
		return 0, nil
	}
	defer func() { _ = r.locker.Release(context.Background(), "ocm:jobs:recovery:lock", token) }()

	lockedBefore := r.now().UTC().Add(-r.lease)
	recovered, err := r.store.RequeueExpiredRunningJobs(ctx, null.TimeFrom(lockedBefore))
	if err != nil {
		return 0, fmt.Errorf("回收过期 worker 锁: %w", err)
	}
	return recovered, nil
}

// tickOnce 把单轮错误转成日志，确保下一周期仍可继续恢复。
func (r *JobRecovery) tickOnce(ctx context.Context) {
	recovered, err := r.Tick(ctx)
	if err != nil {
		r.logger.Error("job recovery 扫描失败", "error", err)
		return
	}
	if recovered > 0 {
		r.logger.Warn("已回收过期后台任务锁", "count", recovered, "lease", r.lease)
	}
}
