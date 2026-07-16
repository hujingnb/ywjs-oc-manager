// Package reaper 周期扫描"5 个 init 子状态下连续 90s 无更新"的孤儿 app,
// 重置 status 并重新入队 app_initialize job。
//
// 多 manager 部署时通过 Redis 锁 ocm:reaper:lock(TTL 30s)互斥,
// 每个 tick 只有一个实例真正扫描;锁 TTL > 单次 reap 预期耗时,
// 持锁实例崩溃时 30s 后由其他实例自然接管。
package reaper

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"oc-manager/internal/domain"
	ocredis "oc-manager/internal/redis"
	"oc-manager/internal/store/sqlc"
	"oc-manager/internal/worker/jobutil"
)

// Store 是 reaper 需要的最小数据访问能力。
// 由 sqlc 生成的 *sqlc.Queries 直接满足本接口,装配时无需 adapter。
type Store interface {
	// ListStaleInits 扫 init 子状态下「连续 staleSeconds 秒无更新」的孤儿 apps。
	// 阈值由 SQL 侧 now()-INTERVAL 计算（见 query 注释），参数为秒数；sqlc 推断为 interface{}。
	ListStaleInits(ctx context.Context, staleSeconds interface{}) ([]sqlc.ListStaleInitsRow, error)
	// SetAppStatus reaper 强制把孤儿 status 回退到 pulling_runtime_image;不走状态机校验。
	SetAppStatus(ctx context.Context, arg sqlc.SetAppStatusParams) error
	// ClearAppProgress 清空 progress_current / progress_total,避免前端继续看到旧值。
	ClearAppProgress(ctx context.Context, id string) error
	// GetLatestAppInitJob 取最近一份 app_initialize job;不存在返回 sql.ErrNoRows。
	GetLatestAppInitJob(ctx context.Context, appID string) (sqlc.Job, error)
	// RequeueJob 把 running / succeeded 的 job 重置回 pending。
	RequeueJob(ctx context.Context, id string) error
	// CreateJob 没有历史 job 时新建一份。
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) error
}

// JobNotifier 与 internal/redis ZSET queue 一致;reaper 重置 job 后通知 scheduler 立即拾取。
// 通知失败仅 log,scheduler 兜底扫表会兜底捡起。
type JobNotifier interface {
	Enqueue(ctx context.Context, jobID string) error
}

// Reaper 持锁、扫描、重置三件套。
type Reaper struct {
	store    Store
	notifier JobNotifier
	locker   ocredis.DistLocker
	logger   *slog.Logger

	// staleAfter 单条 app 距离上次 updated_at 多久仍停留在 init 子状态视为孤儿。
	// 默认 90s,是 progressReporter 1s 节流的约 100 倍冗余,避免阶段切换瞬时停顿误判。
	staleAfter time.Duration
	// lockTTL Redis 锁过期时间,> 单次 reap 预期耗时;持锁实例崩溃 TTL 后被其他实例自然接管。
	lockTTL time.Duration
	// tick 两次扫描之间的间隔。
	tick time.Duration
	// instanceID 拼进 lock token,审计 / 排障可追溯持锁来源。
	instanceID string
}

// New 创建 Reaper;instanceID 推荐复用 main.go 的 manager 进程 UUID,
// 让 Redis 锁 token 含进程身份,审计 / 排障可追溯。
func New(store Store, notifier JobNotifier, locker ocredis.DistLocker, instanceID string, logger *slog.Logger) *Reaper {
	return &Reaper{
		store:      store,
		notifier:   notifier,
		locker:     locker,
		logger:     logger,
		staleAfter: 90 * time.Second,
		lockTTL:    30 * time.Second,
		tick:       60 * time.Second,
		instanceID: instanceID,
	}
}

// Start 启动后台 goroutine:进程启动时立刻跑一次,然后每 60s tick。
// ctx 取消即退出;调用方负责生命周期。
func (r *Reaper) Start(ctx context.Context) {
	go func() {
		// 进程刚起立刻跑一次,接管自己上次留下的孤儿。
		r.tickOnce(ctx)
		ticker := time.NewTicker(r.tick)
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

// tickOnce 抢锁 → reapOnce → 释放锁。任何错误仅记日志,不中断后续 tick。
func (r *Reaper) tickOnce(ctx context.Context) {
	const lockKey = "ocm:reaper:lock"
	token := r.instanceID + ":" + nowToken()
	got, err := r.locker.TryAcquire(ctx, lockKey, token, r.lockTTL)
	if err != nil {
		r.logger.Error("reaper 抢锁失败", "error", err)
		return
	}
	if !got {
		// 其他实例正在跑,本轮跳过。
		return
	}
	// Release 用 context.Background():父 ctx 已取消时仍要尝试释放,
	// 否则崩溃 / 关停时锁残留到 TTL,延迟其他实例接管。
	defer func() { _ = r.locker.Release(context.Background(), lockKey, token) }()
	if err := r.reapOnce(ctx); err != nil {
		r.logger.Error("reaper 单轮扫描失败", "error", err)
	}
}

// reapOnce 单次扫描:取所有 updated_at 落后阈值的 init 子状态行,
// 逐条重置 status 并入队 job。任意一条失败不中断剩余处理,只记日志。
// 阈值改为按秒传给 SQL 侧 now()-INTERVAL 计算（不再用 Go 侧 time.Now()），
// 让「当前时间」与 updated_at（now() 写入）同源同时区，避免跨时区比较错位。
func (r *Reaper) reapOnce(ctx context.Context) error {
	staleSeconds := int64(r.staleAfter / time.Second)
	rows, err := r.store.ListStaleInits(ctx, staleSeconds)
	if err != nil {
		return fmt.Errorf("查询孤儿 apps: %w", err)
	}
	for _, row := range rows {
		if err := r.reapApp(ctx, row); err != nil {
			r.logger.Error("reaper 重置单个 app 失败",
				"app_id", row.ID,
				"status", row.Status,
				"error", err,
			)
		}
	}
	return nil
}

// reapApp 重置 app status 到 pulling_runtime_image + 清空进度 + 重置 / 新建 job + 通知队列。
// reset 不走 EnsureAppTransition(可能从 starting 直接跳回 pulling_runtime_image,
// 不是状态机正常路径,但 reaper 是显式接管,直接强制 SET);
// 状态机校验只针对 worker 阶段切换,reaper 是带外接管动作,与之是不同语义。
func (r *Reaper) reapApp(ctx context.Context, row sqlc.ListStaleInitsRow) error {
	if err := r.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{
		ID:     row.ID,
		Status: domain.AppStatusPullingRuntimeImage,
	}); err != nil {
		return fmt.Errorf("重置 status: %w", err)
	}
	if err := r.store.ClearAppProgress(ctx, row.ID); err != nil {
		return fmt.Errorf("清空 progress_*: %w", err)
	}
	// 复用共享 helper 入队 app_initialize job（与 reconciler 兜底同一逻辑，单点维护）。
	jobID, err := jobutil.EnsureInitJob(ctx, r.store, row.ID)
	if err != nil {
		return err
	}
	if err := r.notifier.Enqueue(ctx, jobID); err != nil {
		// 通知失败仅记账,scheduler 兜底扫表会拾起。
		r.logger.Warn("reaper 入队失败,等 scheduler 兜底", "job_id", jobID, "error", err)
	}
	return nil
}

// nowToken 给 lock token 加一个时间戳后缀,避免同进程在锁过期边缘场景下
// 复用同一 token 字符串造成 token 撞车。
func nowToken() string {
	return time.Now().UTC().Format("20060102150405.000000")
}
