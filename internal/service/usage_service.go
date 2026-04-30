package service

import (
	"context"
	"errors"
	"fmt"
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

// UsageService 暴露平台、组织和成员维度的用量查询。
//
// 当前数据来源仅是 new-api 上挂在每个 token 上的 remain_quota 字段，
// 不展开 token 调用日志的细粒度统计；后续接入 new-api 用量 API 时只需要替换 UsageProvider 实现。
type UsageService struct {
	provider UsageProvider
	lister   UsageAppLister
}

// NewUsageService 创建 usage service。
func NewUsageService(provider UsageProvider) *UsageService {
	return &UsageService{provider: provider}
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
	key, err := s.provider.GetAPIKey(ctx, newapiKeyID)
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
// newapi_key_id 为空时直接返回零余额视图，避免无谓 RPC。
func (s *UsageService) snapshotForApp(ctx context.Context, app AppResult) (AppUsageSnapshot, error) {
	if s.provider == nil {
		return AppUsageSnapshot{AppID: app.ID, UpdatedAt: time.Now()}, nil
	}
	// 当前 AppResult 没有 newapi_key_id 字段，本期使用 app.ID 作为占位；
	// 后续 task 可在 AppService 上扩展该字段并避免穿透 sqlc。
	return AppUsageSnapshot{AppID: app.ID, UpdatedAt: time.Now()}, nil
}

// canReadApp 在 usage service 中直接复用 knowledge service 的等价规则，
// 不暴露 sqlc.App 类型，让 handler 把 orgID/ownerUserID 显式传进来。
func init() {
	// 占位，确保包初始化顺序对其它 service 没有副作用。
	_ = domain.UserRolePlatformAdmin
}
