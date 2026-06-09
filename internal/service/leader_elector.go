package service

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

// leaderLocker 是 LeaderElector 依赖的最小分布式锁接口,只取选举所需的三个方法。
// *redis.RedisDistLocker 在结构上满足此接口,单元测试可用内存 fake 替换,无需真实 Redis。
type leaderLocker interface {
	// TryAcquire 用 SET NX 抢锁:锁空闲时返回 true 并占有,否则返回 false。
	TryAcquire(ctx context.Context, key, token string, ttl time.Duration) (bool, error)
	// Refresh 仅在 token 仍为持有者时续租 TTL,返回 true;不再是持有者返回 false。
	Refresh(ctx context.Context, key, token string, ttl time.Duration) (bool, error)
	// Release 在 token 匹配时主动释放锁,加速其它副本接管。
	Release(ctx context.Context, key, token string) error
}

// LeaderElector 基于 Redis 锁选出单一 leader 副本;非 leader 不运行定时任务。
// 通过 token 续租维持当选,leader 崩溃后租约到期(≤lease)其它副本接管。
type LeaderElector struct {
	locker   leaderLocker
	key      string
	token    string
	lease    time.Duration // 锁 TTL
	interval time.Duration // 续租/重试间隔(应 < lease,如 lease/3)
	isLeader atomic.Bool
}

// NewLeaderElector 创建选举器。token 应在副本生命周期内唯一稳定(如启动时生成的 UUID),
// 以便 Refresh/Release 能凭 token 识别"锁是否仍归本副本"。
func NewLeaderElector(locker leaderLocker, key, token string, lease, interval time.Duration) *LeaderElector {
	return &LeaderElector{
		locker:   locker,
		key:      key,
		token:    token,
		lease:    lease,
		interval: interval,
	}
}

// IsLeader 返回当前副本是否为 leader,供定时任务在每轮开始前 gate。
func (e *LeaderElector) IsLeader() bool { return e.isLeader.Load() }

// Run 阻塞运行选举循环,直到 ctx 取消;退出时若为 leader 主动释放,加速接管。
// 每个 interval tick:
//   - 已是 leader:Refresh 续租,失败或不再持有则降级为非 leader(锁可能因 GC 暂停等丢失)。
//   - 非 leader:TryAcquire 抢锁,成功则升级为 leader。
//
// 任何锁操作返回的 error 都按"本轮未能维持/获得领导权"处理,等下一轮重试,不中断循环;
// scheduler 兜底重试,故单轮失败不致命。
func (e *LeaderElector) Run(ctx context.Context, logger *slog.Logger) error {
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()

	// 退出时若仍为 leader,用未取消的 background ctx 主动释放锁(此时入参 ctx 已取消无法发请求),
	// 让其它副本无需等租约自然过期即可接管。
	defer func() {
		if e.isLeader.Swap(false) {
			_ = e.locker.Release(context.Background(), e.key, e.token)
			logger.Info("leader elector 退出并释放领导权", "key", e.key, "token", e.token)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			e.tick(ctx, logger)
		}
	}
}

// tick 执行单轮选举状态机:根据当前是否为 leader 分别走续租或抢锁路径,并在状态翻转时记日志。
func (e *LeaderElector) tick(ctx context.Context, logger *slog.Logger) {
	if e.isLeader.Load() {
		// 已是 leader:续租失败(出错或锁已不归本副本)即视为失去领导权。
		renewed, err := e.locker.Refresh(ctx, e.key, e.token, e.lease)
		if err != nil || !renewed {
			e.isLeader.Store(false)
			logger.Info("leader elector 失去领导权", "key", e.key, "token", e.token, "renewed", renewed, "error", err)
		}
		return
	}

	// 非 leader:尝试抢锁,成功即当选。出错按未当选处理,下一轮重试。
	acquired, err := e.locker.TryAcquire(ctx, e.key, e.token, e.lease)
	if err != nil {
		return
	}
	if acquired {
		e.isLeader.Store(true)
		logger.Info("leader elector 成为 leader", "key", e.key, "token", e.token)
	}
}
