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
// 余额聚合通过注入 UsageService.GetPlatformUsage 实现，避免重复实现 token 遍历。
type PlatformOverviewService struct {
	store PlatformOverviewStore
	usage *UsageService
}

// NewPlatformOverviewService 创建服务。
// usage 可为 nil（未配置 new-api 时降级），返回的 TotalRemainQuota 为 0。
func NewPlatformOverviewService(store PlatformOverviewStore, usage *UsageService) *PlatformOverviewService {
	return &PlatformOverviewService{store: store, usage: usage}
}

// PlatformOverview 是 GET /platform/overview 的响应视图。
type PlatformOverview struct {
	OrganizationCount int64 `json:"organization_count"`
	MemberCount       int64 `json:"member_count"`
	AppCount          int64 `json:"app_count"`
	RunningAppCount   int64 `json:"running_app_count"`
	ErrorAppCount     int64 `json:"error_app_count"`
	TotalRemainQuota  int64 `json:"total_remain_quota"`
	UsageAvailable    bool  `json:"usage_available"`
}

// Get 拉取平台总览。usage 不可用时 UsageAvailable=false，前端展示提示。
func (s *PlatformOverviewService) Get(ctx context.Context, principal auth.Principal) (PlatformOverview, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return PlatformOverview{}, ErrForbidden
	}
	out := PlatformOverview{}
	orgs, err := s.store.CountActiveOrganizations(ctx)
	if err != nil {
		return PlatformOverview{}, fmt.Errorf("查询组织计数失败: %w", err)
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
	if s.usage != nil {
		view, err := s.usage.GetPlatformUsage(ctx, principal)
		if err == nil {
			out.TotalRemainQuota = view.TotalRemainQuota
			out.UsageAvailable = true
		}
	}
	return out, nil
}
