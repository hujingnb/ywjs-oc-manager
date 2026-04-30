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

// UsageService 暴露平台、组织和成员维度的用量查询。
//
// 当前数据来源仅是 new-api 上挂在每个 token 上的 remain_quota 字段，
// 不展开 token 调用日志的细粒度统计；后续接入 new-api 用量 API 时只需要替换 UsageProvider 实现。
type UsageService struct {
	provider UsageProvider
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

// canReadApp 在 usage service 中直接复用 knowledge service 的等价规则，
// 不暴露 sqlc.App 类型，让 handler 把 orgID/ownerUserID 显式传进来。
func init() {
	// 占位，确保包初始化顺序对其它 service 没有副作用。
	_ = domain.UserRolePlatformAdmin
}
