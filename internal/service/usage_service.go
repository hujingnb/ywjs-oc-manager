package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
)

// ErrUsageUnavailable 表示底层 new-api 不可用，用量数据暂时无法返回。
var ErrUsageUnavailable = errors.New("用量服务暂不可用")

// UsageProvider 抽象 manager 与 new-api 用量查询之间的桥接，便于测试中替换为内存桩。
type UsageProvider interface {
	GetAPIKey(ctx context.Context, id int64) (newapi.APIKey, error)
}

// UsageAppLister 抽象按维度列出应用的能力，用于多维度聚合。
// 仅暴露 service 真正需要的字段：app_id、newapi_key_id、org_id、owner_user_id。
type UsageAppLister interface {
	ListByOrg(ctx context.Context, principal auth.Principal, orgID string, limit, offset int32) ([]AppResult, error)
}

// UsageOrgLister 抽象列出全部组织的能力，用于平台维度聚合。
// 仅暴露 service 真正需要的字段：org_id；平台维度需要遍历组织。
type UsageOrgLister interface {
	ListOrganizations(ctx context.Context, principal auth.Principal, limit, offset int32) ([]OrganizationResult, error)
}

// usageCacheTTL 控制 provider.GetAPIKey 调用的进程内缓存窗口。
// 5s 既能让 UI 4s 轮询每次基本拿到上次结果，又能避免 token 数百时把 new-api 打爆。
const usageCacheTTL = 5 * time.Second

type usageCacheEntry struct {
	key    newapi.APIKey
	err    error
	expire time.Time
}

// UsageService 暴露平台、组织和成员维度的用量查询。
//
// 当前数据来源仅是 new-api 上挂在每个 token 上的 remain_quota 字段，
// 不展开 token 调用日志的细粒度统计；后续接入 new-api 用量 API 时只需要替换 UsageProvider 实现。
type UsageService struct {
	provider UsageProvider
	lister   UsageAppLister
	orgs     UsageOrgLister

	cacheMu sync.Mutex
	cache   map[int64]usageCacheEntry
}

// NewUsageService 创建 usage service。
func NewUsageService(provider UsageProvider) *UsageService {
	return &UsageService{provider: provider, cache: make(map[int64]usageCacheEntry)}
}

// AppUsageSnapshot 是单个应用维度的用量视图。
type AppUsageSnapshot struct {
	AppID       string    `json:"app_id"`
	NewapiKeyID int64     `json:"newapi_key_id"`
	RemainQuota int64     `json:"remain_quota"`
	Status      int       `json:"status"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// GetAppUsage 拉取指定 token 的用量。
//
// 权限规则：
//   - 平台管理员可查任意应用；
//   - 组织管理员可查本组织应用，调用方需要在 handler 层补 orgID 校验；
//   - 组织成员仅能查询自己的应用。
// 这里 service 接受调用方决策好的 ownerOrgID/ownerUserID 上下文，避免穿透 sqlc 类型。
func (s *UsageService) GetAppUsage(ctx context.Context, principal auth.Principal, appID, ownerOrgID, ownerUserID string, newapiKeyID int64) (AppUsageSnapshot, error) {
	if !canReadApp(principal, ownerOrgID, ownerUserID) {
		return AppUsageSnapshot{}, ErrForbidden
	}
	if s.provider == nil {
		return AppUsageSnapshot{}, ErrUsageUnavailable
	}
	if newapiKeyID == 0 {
		return AppUsageSnapshot{AppID: appID, NewapiKeyID: 0, UpdatedAt: time.Now()}, nil
	}
	key, err := s.fetchAPIKey(ctx, newapiKeyID)
	if err != nil {
		switch {
		case errors.Is(err, newapi.ErrNotFound):
			return AppUsageSnapshot{}, ErrNotFound
		case errors.Is(err, newapi.ErrUnauthorized):
			return AppUsageSnapshot{}, ErrUsageUnavailable
		default:
			return AppUsageSnapshot{}, fmt.Errorf("查询用量失败: %w", err)
		}
	}
	return AppUsageSnapshot{
		AppID:       appID,
		NewapiKeyID: key.ID,
		RemainQuota: key.RemainQuota,
		Status:      key.Status,
		UpdatedAt:   time.Now(),
	}, nil
}

// fetchAPIKey 包裹 provider.GetAPIKey 加 5s 进程内缓存。
// 缓存命中（无论成功或失败）都直接返回，避免 token 数量大时把 new-api 打爆。
func (s *UsageService) fetchAPIKey(ctx context.Context, id int64) (newapi.APIKey, error) {
	s.cacheMu.Lock()
	if entry, ok := s.cache[id]; ok && time.Now().Before(entry.expire) {
		s.cacheMu.Unlock()
		return entry.key, entry.err
	}
	s.cacheMu.Unlock()

	key, err := s.provider.GetAPIKey(ctx, id)
	s.cacheMu.Lock()
	s.cache[id] = usageCacheEntry{key: key, err: err, expire: time.Now().Add(usageCacheTTL)}
	s.cacheMu.Unlock()
	return key, err
}

// AggregatedUsage 是组织/平台维度的用量汇总视图。
type AggregatedUsage struct {
	Scope            string             `json:"scope"`
	ScopeID          string             `json:"scope_id,omitempty"`
	TotalRemainQuota int64              `json:"total_remain_quota"`
	Apps             []AppUsageSnapshot `json:"apps"`
	UpdatedAt        time.Time          `json:"updated_at"`
}

// SetAppLister 注入应用列表来源。多维度聚合 (org/member/platform) 必须先注入。
func (s *UsageService) SetAppLister(lister UsageAppLister) {
	s.lister = lister
}

// SetOrgLister 注入组织列表来源。仅平台维度聚合需要。
func (s *UsageService) SetOrgLister(orgs UsageOrgLister) {
	s.orgs = orgs
}

// GetMemberUsage 返回某成员名下应用的用量聚合（实际只返回该成员的单个应用，因 schema 上 member↔app 唯一）。
func (s *UsageService) GetMemberUsage(ctx context.Context, principal auth.Principal, orgID, memberID string) (AggregatedUsage, error) {
	if s.lister == nil {
		return AggregatedUsage{}, ErrUsageUnavailable
	}
	apps, err := s.lister.ListByOrg(ctx, principal, orgID, 200, 0)
	if err != nil {
		return AggregatedUsage{}, err
	}
	view := AggregatedUsage{Scope: "member", ScopeID: memberID, UpdatedAt: time.Now()}
	for _, app := range apps {
		if app.OwnerUserID != memberID {
			continue
		}
		snap, err := s.snapshotForApp(ctx, app)
		if err != nil {
			return AggregatedUsage{}, err
		}
		view.Apps = append(view.Apps, snap)
		view.TotalRemainQuota += snap.RemainQuota
	}
	return view, nil
}

// GetOrgUsage 返回组织维度全部应用的用量聚合。
func (s *UsageService) GetOrgUsage(ctx context.Context, principal auth.Principal, orgID string) (AggregatedUsage, error) {
	if s.lister == nil {
		return AggregatedUsage{}, ErrUsageUnavailable
	}
	apps, err := s.lister.ListByOrg(ctx, principal, orgID, 500, 0)
	if err != nil {
		return AggregatedUsage{}, err
	}
	view := AggregatedUsage{Scope: "organization", ScopeID: orgID, UpdatedAt: time.Now()}
	for _, app := range apps {
		snap, err := s.snapshotForApp(ctx, app)
		if err != nil {
			return AggregatedUsage{}, err
		}
		view.Apps = append(view.Apps, snap)
		view.TotalRemainQuota += snap.RemainQuota
	}
	return view, nil
}

// snapshotForApp 把 service.AppResult 翻译成 AppUsageSnapshot；
// newapi_key_id 为空（应用尚未初始化或失败）时直接返回零余额视图，避免无谓 RPC。
// provider 错误（NotFound/Unauthorized）按零余额降级，不阻断聚合。
func (s *UsageService) snapshotForApp(ctx context.Context, app AppResult) (AppUsageSnapshot, error) {
	if s.provider == nil || app.NewapiKeyID == 0 {
		return AppUsageSnapshot{AppID: app.ID, NewapiKeyID: app.NewapiKeyID, UpdatedAt: time.Now()}, nil
	}
	key, err := s.fetchAPIKey(ctx, app.NewapiKeyID)
	if err != nil {
		// 单 token 失败不应该让整个聚合崩溃，降级返回零余额，scope 维度自然偏低。
		if errors.Is(err, newapi.ErrNotFound) || errors.Is(err, newapi.ErrUnauthorized) {
			return AppUsageSnapshot{AppID: app.ID, NewapiKeyID: app.NewapiKeyID, UpdatedAt: time.Now()}, nil
		}
		return AppUsageSnapshot{}, fmt.Errorf("查询 token %d 用量失败: %w", app.NewapiKeyID, err)
	}
	return AppUsageSnapshot{
		AppID:       app.ID,
		NewapiKeyID: key.ID,
		RemainQuota: key.RemainQuota,
		Status:      key.Status,
		UpdatedAt:   time.Now(),
	}, nil
}

// GetPlatformUsage 跨组织聚合所有应用的用量。
// 仅平台管理员可调；调用方在 handler 层做角色校验，service 层再二次拦截一次。
func (s *UsageService) GetPlatformUsage(ctx context.Context, principal auth.Principal) (AggregatedUsage, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return AggregatedUsage{}, ErrForbidden
	}
	if s.orgs == nil || s.lister == nil {
		return AggregatedUsage{}, ErrUsageUnavailable
	}
	orgs, err := s.orgs.ListOrganizations(ctx, principal, 200, 0)
	if err != nil {
		return AggregatedUsage{}, err
	}
	view := AggregatedUsage{Scope: "platform", UpdatedAt: time.Now()}
	for _, org := range orgs {
		apps, err := s.lister.ListByOrg(ctx, principal, org.ID, 500, 0)
		if err != nil {
			return AggregatedUsage{}, err
		}
		for _, app := range apps {
			snap, err := s.snapshotForApp(ctx, app)
			if err != nil {
				return AggregatedUsage{}, err
			}
			view.Apps = append(view.Apps, snap)
			view.TotalRemainQuota += snap.RemainQuota
		}
	}
	return view, nil
}

// canReadApp 在 usage service 中直接复用 knowledge service 的等价规则，
// 不暴露 sqlc.App 类型，让 handler 把 orgID/ownerUserID 显式传进来。
func init() {
	// 占位，确保包初始化顺序对其它 service 没有副作用。
	_ = domain.UserRolePlatformAdmin
}
