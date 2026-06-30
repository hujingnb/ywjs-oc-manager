// Package service - WebPublishSiteService 负责站点管理操作（列表/下线/续期）。
// 所有写操作均校验企业归属权限（CanManageOrg），列表读取校验 CanViewOrg。
// 下线后由 site-server 下一轮同步感知 status=disabled 并停止服务；
// 整站前缀删除为 best-effort（失败不阻断响应，site-server 因 404 对外透明）。
package service

import (
	"context"
	"fmt"
	"time"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// WebPublishSiteStore 是 WebPublishSiteService 需要的最小存储能力抽象。
// 仅包含本服务实际调用的方法，避免强依赖具体 Queries 类型。
type WebPublishSiteStore interface {
	// ListSitesByOrg 返回企业下所有已发布站点，按 updated_at 倒序。
	ListSitesByOrg(ctx context.Context, orgID string) ([]sqlc.PublishedSite, error)
	// GetPublishedSiteByID 按站点 ID 查记录；不存在返回 sql.ErrNoRows。
	GetPublishedSiteByID(ctx context.Context, id string) (sqlc.PublishedSite, error)
	// GetWebPublishConfig 按企业 ID 查 web-publish 配置；不存在返回 sql.ErrNoRows。
	GetWebPublishConfig(ctx context.Context, orgID string) (sqlc.OrgWebPublishConfig, error)
	// SetPublishedSiteStatus 更新站点状态（active/disabled/expired）。
	SetPublishedSiteStatus(ctx context.Context, arg sqlc.SetPublishedSiteStatusParams) error
	// RenewPublishedSite 延后站点到期时间并置回 active 状态。
	RenewPublishedSite(ctx context.Context, arg sqlc.RenewPublishedSiteParams) error
}

// siteObjectStore 是 WebPublishSiteService 需要的对象存储能力子集。
// 单独抽象而非复用 publishObjectStore，保持依赖最小化、测试易注入。
type siteObjectStore interface {
	// DeletePrefix 删除指定前缀下所有对象。
	DeletePrefix(ctx context.Context, prefix string) error
}

// SiteResult 是站点管理接口的统一返回结构，对应单条站点视图。
type SiteResult struct {
	// ID 是站点唯一标识。
	ID string `json:"id"`
	// Host 是完整主机名（slug.base_domain）。
	Host string `json:"host"`
	// URL 是站点完整访问地址（https://Host）。
	URL string `json:"url"`
	// Slug 是站点的 DNS label 部分。
	Slug string `json:"slug"`
	// Status 是站点当前状态（active/disabled/expired）。
	Status string `json:"status"`
	// SizeBytes 是当前版本所有静态资源的字节总和。
	SizeBytes int64 `json:"size_bytes"`
	// CreatedAt 是站点首次发布时间。
	CreatedAt time.Time `json:"created_at"`
	// ExpiresAt 是站点当前到期时间。
	ExpiresAt time.Time `json:"expires_at"`
}

// WebPublishSiteService 提供站点列表/下线/续期管理能力。
// 设计约束：
//   - 所有操作均先校验企业归属权限，防止跨企业操作；
//   - Takedown 删除整站前缀（所有版本），下线由 site-server 同步后生效；
//   - Renew 按企业 site_ttl_days 延后过期时间，并置回 active 状态。
type WebPublishSiteService struct {
	store WebPublishSiteStore
	obj   siteObjectStore
	// now 返回当前时间；生产代码传 nil 时默认使用 time.Now。
	now func() time.Time
}

// NewWebPublishSiteService 创建 WebPublishSiteService。
// now 为 nil 时使用系统时钟 time.Now。
func NewWebPublishSiteService(store WebPublishSiteStore, obj siteObjectStore, now func() time.Time) *WebPublishSiteService {
	if now == nil {
		now = time.Now
	}
	return &WebPublishSiteService{store: store, obj: obj, now: now}
}

// ListByOrg 返回企业下所有站点列表。
// 要求主体有 CanViewOrg 权限，否则返回 ErrForbidden。
func (s *WebPublishSiteService) ListByOrg(ctx context.Context, p auth.Principal, orgID string) ([]SiteResult, error) {
	// 权限检查：平台管理员、本组织管理员和成员均可查看企业站点列表。
	if !auth.CanViewOrg(p, orgID) {
		return nil, ErrForbidden
	}

	sites, err := s.store.ListSitesByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("查询企业站点列表失败: %w", err)
	}

	// 将 DB 行映射为 SiteResult 视图结构。
	results := make([]SiteResult, 0, len(sites))
	for _, site := range sites {
		results = append(results, toSiteResult(site))
	}
	return results, nil
}

// Takedown 将站点状态置为 disabled，并删除整站对象前缀（所有版本）。
// 要求主体有 CanManageOrg 权限（即对站点所属企业有写权限），否则返回 ErrForbidden。
// 对象删除为 best-effort：失败不阻断响应，site-server 因 404 对外透明，下次同步自然生效。
func (s *WebPublishSiteService) Takedown(ctx context.Context, p auth.Principal, siteID string) error {
	// 加载站点并校验企业归属权限。
	site, err := s.authorizeSite(ctx, p, siteID)
	if err != nil {
		return err
	}

	// 将站点状态置为 disabled；site-server 下轮同步后停止对外服务。
	if err := s.store.SetPublishedSiteStatus(ctx, sqlc.SetPublishedSiteStatusParams{
		Status: domain.SiteStatusDisabled,
		ID:     siteID,
	}); err != nil {
		return fmt.Errorf("更新站点状态失败: %w", err)
	}

	// 删除整站根前缀下所有对象（包含所有历史版本），而非仅当前版本前缀。
	// 删除失败不阻断响应：DB 状态已置 disabled，site-server 收到 404 后自动停服。
	_ = s.obj.DeletePrefix(ctx, siteRootPrefix(site.ID))
	return nil
}

// Renew 按企业 site_ttl_days 延后站点到期时间，并将状态置回 active。
// 要求主体有 CanManageOrg 权限，否则返回 ErrForbidden。
func (s *WebPublishSiteService) Renew(ctx context.Context, p auth.Principal, siteID string) error {
	// 加载站点并校验企业归属权限。
	site, err := s.authorizeSite(ctx, p, siteID)
	if err != nil {
		return err
	}

	// 查企业 web-publish 配置，取 site_ttl_days 计算新到期时间。
	cfg, err := s.store.GetWebPublishConfig(ctx, site.OrgID)
	if err != nil {
		return fmt.Errorf("查询企业 web-publish 配置失败: %w", err)
	}

	// 新到期时间 = 当前时间 + 企业配置的 TTL 天数。
	newExpiresAt := s.now().Add(time.Duration(cfg.SiteTtlDays) * 24 * time.Hour)

	if err := s.store.RenewPublishedSite(ctx, sqlc.RenewPublishedSiteParams{
		ExpiresAt: newExpiresAt,
		ID:        siteID,
	}); err != nil {
		return fmt.Errorf("续期站点失败: %w", err)
	}
	return nil
}

// authorizeSite 按 siteID 加载站点记录，并校验主体对站点所属企业是否有写权限。
// 站点不存在时返回 ErrNotFound；无权限时返回 ErrForbidden。
func (s *WebPublishSiteService) authorizeSite(ctx context.Context, p auth.Principal, siteID string) (sqlc.PublishedSite, error) {
	site, err := s.store.GetPublishedSiteByID(ctx, siteID)
	if err != nil {
		return sqlc.PublishedSite{}, fmt.Errorf("查询站点失败: %w", err)
	}

	// CanManageOrg 校验主体对站点所属企业的写权限：
	// 平台管理员可管任意企业，企业管理员只能管理本企业。
	if !auth.CanManageOrg(p, site.OrgID) {
		return sqlc.PublishedSite{}, ErrForbidden
	}
	return site, nil
}

// siteRootPrefix 返回站点在对象存储中的根前缀（包含所有版本目录）。
// 格式：published-sites/<siteID>/（末尾带 /）。
func siteRootPrefix(siteID string) string {
	return fmt.Sprintf("published-sites/%s/", siteID)
}

// toSiteResult 将 sqlc.PublishedSite DB 行映射为 SiteResult 视图结构。
func toSiteResult(site sqlc.PublishedSite) SiteResult {
	return SiteResult{
		ID:        site.ID,
		Host:      site.Host,
		URL:       "https://" + site.Host,
		Slug:      site.Slug,
		Status:    site.Status,
		SizeBytes: site.SizeBytes,
		CreatedAt: site.CreatedAt,
		ExpiresAt: site.ExpiresAt,
	}
}
