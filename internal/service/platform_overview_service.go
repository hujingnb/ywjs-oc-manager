package service

import (
	"context"
	"fmt"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// PlatformOverviewStore 是 PlatformOverviewService 需要的 sqlc 子集。
type PlatformOverviewStore interface {
	CountActiveOrganizations(ctx context.Context) (int64, error)
	CountActiveUsers(ctx context.Context) (int64, error)
	CountAppsByStatus(ctx context.Context) ([]sqlc.CountAppsByStatusRow, error)
}

// PlatformOverviewService 提供平台总览页所需的聚合数据。
//
// 仅 platform_admin 可调；service 层做角色校验，handler 也做一次。
// 用量数据由前端单独调 GET /api/v1/usage/platform 获取（new-api 直接返回按日 quota），
// platform overview 不再二次代理"全平台余额合计"。
type PlatformOverviewService struct {
	store PlatformOverviewStore
}

// NewPlatformOverviewService 创建服务。
func NewPlatformOverviewService(store PlatformOverviewStore) *PlatformOverviewService {
	return &PlatformOverviewService{store: store}
}

// PlatformOverview 是 GET /platform/overview 的响应视图。
//
// 用量字段（TotalRemainQuota / UsageAvailable）已下线：前端用单独的 usage 接口拿日级 quota 数据，
// 总览卡只展示组织 / 成员 / 应用计数。保留 JSON 字段名是为了前端旧代码继续解析（值始终为 0/false）。
type PlatformOverview struct {
	OrganizationCount int64 `json:"organization_count"`
	MemberCount       int64 `json:"member_count"`
	AppCount          int64 `json:"app_count"`
	RunningAppCount   int64 `json:"running_app_count"`
	ErrorAppCount     int64 `json:"error_app_count"`
	TotalRemainQuota  int64 `json:"total_remain_quota"`
	UsageAvailable    bool  `json:"usage_available"`
}

// Get 拉取平台总览。
func (s *PlatformOverviewService) Get(ctx context.Context, principal auth.Principal) (PlatformOverview, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return PlatformOverview{}, ErrForbidden
	}
	out := PlatformOverview{}
	orgs, err := s.store.CountActiveOrganizations(ctx)
	if err != nil {
		return PlatformOverview{}, fmt.Errorf("查询企业计数失败: %w", err)
	}
	out.OrganizationCount = orgs
	members, err := s.store.CountActiveUsers(ctx)
	if err != nil {
		return PlatformOverview{}, fmt.Errorf("查询成员计数失败: %w", err)
	}
	out.MemberCount = members
	rows, err := s.store.CountAppsByStatus(ctx)
	if err != nil {
		return PlatformOverview{}, fmt.Errorf("查询应用计数失败: %w", err)
	}
	for _, r := range rows {
		out.AppCount += r.Count
		switch r.Status {
		case domain.AppStatusRunning:
			out.RunningAppCount = r.Count
		case domain.AppStatusError:
			out.ErrorAppCount = r.Count
		}
	}
	return out, nil
}
