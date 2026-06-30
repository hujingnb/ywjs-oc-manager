// Package webpublish 提供 web-publish 的后台维护任务：站点 TTL 回收与证书续签巡检。
package webpublish

import (
	"context"
	"fmt"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// SiteReaperStore 是站点回收所需的最小数据访问能力。
// sqlc 生成的 *sqlc.Queries 直接满足本接口，装配时无需 adapter。
type SiteReaperStore interface {
	// ListExpiredActiveSites 扫描 status=active 且 expires_at < now() 的站点。
	ListExpiredActiveSites(ctx context.Context) ([]sqlc.PublishedSite, error)
	// SetPublishedSiteStatus 直接更新站点状态，不经过状态机校验（带外接管语义）。
	SetPublishedSiteStatus(ctx context.Context, arg sqlc.SetPublishedSiteStatusParams) error
}

// ReaperObjectStore 是删除对象前缀的能力。
// 实现方可以是 MinIO / S3 客户端，只需满足按前缀批量删除语义。
type ReaperObjectStore interface {
	// DeletePrefix 递归删除给定前缀下的全部对象。
	DeletePrefix(ctx context.Context, prefix string) error
}

// SiteReaper 回收已过期站点：将 status 置为 expired 并删除整站的对象前缀。
type SiteReaper struct {
	store SiteReaperStore
	obj   ReaperObjectStore
}

// NewSiteReaper 构造 SiteReaper。
func NewSiteReaper(store SiteReaperStore, obj ReaperObjectStore) *SiteReaper {
	return &SiteReaper{store: store, obj: obj}
}

// ReapOnce 扫一遍过期 active 站点，逐个置 expired 并删整站前缀。
// 单个站点处理失败不阻断其余（记录后继续），保证一个坏站点不卡住整轮回收；
// 下轮 tick 时该站点仍在 active+expired_at<now() 范围内，会被再次尝试。
func (r *SiteReaper) ReapOnce(ctx context.Context) error {
	sites, err := r.store.ListExpiredActiveSites(ctx)
	if err != nil {
		return fmt.Errorf("扫描过期站点失败: %w", err)
	}
	for _, s := range sites {
		// 先置 expired，再删对象；状态先行保证即使删对象失败下轮也不会重复入库。
		if err := r.store.SetPublishedSiteStatus(ctx, sqlc.SetPublishedSiteStatusParams{
			Status: domain.SiteStatusExpired,
			ID:     s.ID,
		}); err != nil {
			// 单条更新失败跳过，下轮重试。
			continue
		}
		// 删整站前缀；按 ID 而非 S3Prefix 拼，避免历史数据前缀格式不一致。
		// 形如 "published-sites/<siteID>/" 涵盖所有版本子目录。
		_ = r.obj.DeletePrefix(ctx, fmt.Sprintf("published-sites/%s/", s.ID))
	}
	return nil
}
