// Package webpublish — cert_renewal.go
// CertRenewalChecker 扫描临近到期的通配证书，逐个入队 provision job 完成自动续签。
// 只做"巡检 + 入队"，不直接操作证书或 k8s 资源，实际续签由已有 provision handler 执行（DRY）。
package webpublish

import (
	"context"
	"log/slog"
	"time"

	null "github.com/guregu/null/v5"

	ocredis "oc-manager/internal/redis"
	"oc-manager/internal/store/sqlc"
)

// CertRenewalStore 是 CertRenewalChecker 所需的最小存储能力。
// sqlc 生成的 *sqlc.Queries 直接满足本接口，装配时无需 adapter。
type CertRenewalStore interface {
	// ListConfigsCertExpiringBefore 扫描 cert_not_after < before 的启用配置。
	// before 对应阈值时间点：now() + renewBefore。
	ListConfigsCertExpiringBefore(ctx context.Context, certNotAfter null.Time) ([]sqlc.OrgWebPublishConfig, error)
}

// ProvisionEnqueuer 抽象续签入队能力，由 WebPublishConfigService 实现。
// 单独声明接口避免循环依赖，也便于单测注入 fake。
type ProvisionEnqueuer interface {
	// EnqueueProvision 为指定企业入队一个 web_publish_provision job。
	EnqueueProvision(ctx context.Context, orgID string) error
}

// CertRenewalChecker 巡检临近到期证书并触发续签：
// 计算到期阈值 = now() + renewBefore，查询到期配置后逐个入队 provision job。
// 单个入队失败非致命——跳过并继续，下轮 Loop tick 会重试。
type CertRenewalChecker struct {
	store       CertRenewalStore
	enqueuer    ProvisionEnqueuer
	renewBefore time.Duration  // 提前多久开始续签，默认 30 天
	now         func() time.Time // 可注入，便于单测控制时钟
}

// NewCertRenewalChecker 构造 CertRenewalChecker。
// renewBefore <= 0 时取默认值 30 天；now 为 nil 时使用 time.Now。
func NewCertRenewalChecker(store CertRenewalStore, enqueuer ProvisionEnqueuer, renewBefore time.Duration, now func() time.Time) *CertRenewalChecker {
	if renewBefore <= 0 {
		renewBefore = 30 * 24 * time.Hour // 默认提前 30 天续签
	}
	if now == nil {
		now = time.Now
	}
	return &CertRenewalChecker{
		store:       store,
		enqueuer:    enqueuer,
		renewBefore: renewBefore,
		now:         now,
	}
}

// CheckOnce 执行一轮续签巡检：
// 计算阈值 = now()+renewBefore，查询 cert_not_after < 阈值的配置，
// 为每条配置入队 provision job（单个失败跳过，下轮重试）。
func (c *CertRenewalChecker) CheckOnce(ctx context.Context) error {
	// 计算续签阈值：证书在此时间点前到期的都应触发续签
	threshold := null.TimeFrom(c.now().Add(c.renewBefore))

	rows, err := c.store.ListConfigsCertExpiringBefore(ctx, threshold)
	if err != nil {
		return err
	}

	for _, cfg := range rows {
		// 单个入队失败不中断整轮巡检，下轮 tick 重试
		if err := c.enqueuer.EnqueueProvision(ctx, cfg.OrgID); err != nil {
			// 当前无 logger 依赖，错误静默跳过；Loop 层可扩展日志记录
			continue
		}
	}
	return nil
}

// CertRenewalLoop 在后台周期调用 CertRenewalChecker.CheckOnce，
// 通过 Redis 分布式锁保证多副本互斥。
// 设计对齐 Loop（SiteReaper 版），cmd/server 装配方式相同。
type CertRenewalLoop struct {
	checker    *CertRenewalChecker
	locker     ocredis.DistLocker
	logger     *slog.Logger
	instanceID string

	// lockTTL Redis 锁过期时间，须大于单次 CheckOnce 预期耗时。
	lockTTL time.Duration
	// tick 两次扫描之间的间隔，默认 12 小时。
	tick time.Duration
}

// NewCertRenewalLoop 构造 CertRenewalLoop。instanceID 推荐复用 main.go 进程 UUID，
// 使 Redis 锁 token 含进程身份，便于审计 / 排障。
func NewCertRenewalLoop(checker *CertRenewalChecker, locker ocredis.DistLocker, instanceID string, logger *slog.Logger) *CertRenewalLoop {
	return &CertRenewalLoop{
		checker:    checker,
		locker:     locker,
		logger:     logger,
		instanceID: instanceID,
		lockTTL:    30 * time.Minute,
		tick:       12 * time.Hour,
	}
}

// Start 启动后台 goroutine：进程启动时立刻跑一次，然后每 12h tick。
// ctx 取消即退出；调用方负责生命周期。
func (l *CertRenewalLoop) Start(ctx context.Context) {
	go func() {
		// 进程刚启动立刻跑一次，避免等待第一个 tick 周期
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

// tickOnce 抢 Redis 锁 → CheckOnce → 释放锁。任何错误仅记日志，不中断后续 tick。
func (l *CertRenewalLoop) tickOnce(ctx context.Context) {
	const lockKey = "ocm:webpublish-cert-renewal:lock"
	token := l.instanceID + ":" + nowWebPublishToken() // 复用 loop.go 中的辅助函数
	got, err := l.locker.TryAcquire(ctx, lockKey, token, l.lockTTL)
	if err != nil {
		l.logger.Error("cert renewal 抢锁失败", "error", err)
		return
	}
	if !got {
		// 其他副本正在跑，本轮跳过
		return
	}
	// 父 ctx 已取消时仍要尝试释放锁，否则锁残留到 TTL 才能被其他副本接管
	defer func() { _ = l.locker.Release(context.Background(), lockKey, token) }()
	if err := l.checker.CheckOnce(ctx); err != nil {
		l.logger.Error("cert renewal 单轮巡检失败", "error", err)
	}
}
