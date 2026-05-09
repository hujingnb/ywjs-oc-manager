package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/store/sqlc"
)

// ErrUsageUnavailable 表示底层 new-api 不可用，用量数据暂时无法返回。
var ErrUsageUnavailable = errors.New("用量服务暂不可用")

// UsageNewAPIClient 是 manager 透传 new-api 用量端点所需的最小集合。
//
// manager 端不再对用量数据做缓存或多维聚合：每个接口直接代理对应的 new-api endpoint，
// 让 new-api 维护一切按 token / user / 平台的 quota 统计与分组。
type UsageNewAPIClient interface {
	GetTokenLogs(ctx context.Context, q newapi.LogsQuery) (newapi.LogsPage, error)
	GetUserQuotaDates(ctx context.Context, userID, since, until int64) ([]newapi.QuotaDate, error)
	GetAllQuotaDates(ctx context.Context, since, until int64) ([]newapi.QuotaDate, error)
}

// UsageStore 是 service 把 manager UUID 转 new-api 数字 id 用到的最小数据访问能力。
type UsageStore interface {
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	GetActiveAppByOwner(ctx context.Context, ownerUserID pgtype.UUID) (sqlc.App, error)
	GetOrganization(ctx context.Context, id pgtype.UUID) (sqlc.Organization, error)
}

// UsageService 提供 4 个维度的用量代理：app / member / organization / platform。
//
// 设计要点：
//   - manager 端 0 缓存、0 聚合——所有维度直接调一个对应 new-api 端点；
//   - 角色权限校验仍在 service 层做（与 knowledge / app service 一致）；
//   - 把 manager UUID 到 new-api 数字 id 的转译集中在本文件，handler 不感知 new-api 数字 id。
type UsageService struct {
	store       UsageStore
	client      UsageNewAPIClient
	failAuditor NewAPIFailureAuditor // 与 OrganizationService 同款；nil 时跳过审计
}

// NewUsageService 创建 usage service。
//
// store 用于 manager UUID → new-api 数字 id 的查询；client 是 newapi.Client 满足的薄接口。
// 任一为 nil 时所有方法返回 ErrUsageUnavailable，便于在 manager 启动时未配 new-api 的场景下 fail-soft。
// failAuditor 为 nil 时静默跳过 new-api 失败审计。
func NewUsageService(store UsageStore, client UsageNewAPIClient, failAuditor NewAPIFailureAuditor) *UsageService {
	return &UsageService{store: store, client: client, failAuditor: failAuditor}
}

// LogsPage 是 app / member 维度的响应：透传 new-api log entries + 分页 total。
type LogsPage struct {
	Scope     string            `json:"scope"`
	ScopeID   string            `json:"scope_id,omitempty"`
	Items     []newapi.LogEntry `json:"items"`
	Total     int               `json:"total"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// QuotaSeries 是 organization / platform 维度的响应：透传 new-api 的按日 quota 汇总。
type QuotaSeries struct {
	Scope     string             `json:"scope"`
	ScopeID   string             `json:"scope_id,omitempty"`
	Items     []newapi.QuotaDate `json:"items"`
	UpdatedAt time.Time          `json:"updated_at"`
}

// LogsQueryOptions 是对外暴露的查询选项；service 不内置默认时间窗（避免和前端分页错位），
// 但 PageSize 缺省 20 与 newapi.LogsQuery 保持一致。
type LogsQueryOptions struct {
	Since     int64
	Until     int64
	Page      int
	PageSize  int
	ModelName string
}

// GetAppUsage 拉指定应用 token 的调用日志（透传 GET /api/log/?token_id=X）。
func (s *UsageService) GetAppUsage(ctx context.Context, principal auth.Principal, appID, ownerOrgID, ownerUserID string, newapiKeyID int64, opts LogsQueryOptions) (LogsPage, error) {
	if !canReadApp(principal, ownerOrgID, ownerUserID) {
		return LogsPage{}, ErrForbidden
	}
	if s.client == nil {
		return LogsPage{}, ErrUsageUnavailable
	}
	if newapiKeyID == 0 {
		return LogsPage{Scope: "app", ScopeID: appID, Items: []newapi.LogEntry{}, UpdatedAt: time.Now()}, nil
	}
	page, err := s.client.GetTokenLogs(ctx, newapi.LogsQuery{
		TokenID:   newapiKeyID,
		Since:     opts.Since,
		Until:     opts.Until,
		Page:      opts.Page,
		PageSize:  opts.PageSize,
		ModelName: opts.ModelName,
	})
	if err != nil {
		if s.failAuditor != nil {
			s.failAuditor.RecordNewAPIFailure(ctx, NewAPIFailureContext{
				ActorID:   principal.UserID,
				ActorRole: principal.Role,
				OrgID:     ownerOrgID,
				Endpoint:  "GET /api/log/?token_id=...",
				Err:       err,
			})
		}
		return LogsPage{}, mapUsageError(err)
	}
	return LogsPage{Scope: "app", ScopeID: appID, Items: page.Items, Total: page.Total, UpdatedAt: time.Now()}, nil
}

// GetMemberUsage 拉成员名下应用的调用日志（按 schema 约束 member↔app 是 1-1）。
func (s *UsageService) GetMemberUsage(ctx context.Context, principal auth.Principal, orgID, memberID string, opts LogsQueryOptions) (LogsPage, error) {
	if s.store == nil {
		return LogsPage{}, ErrUsageUnavailable
	}
	if principal.Role != domain.UserRolePlatformAdmin && principal.OrgID != orgID {
		return LogsPage{}, ErrForbidden
	}
	// 普通成员只允许查询自己名下的用量；其他成员的用量属于成员视角，对其不可见。
	if principal.Role == domain.UserRoleOrgMember && principal.UserID != memberID {
		return LogsPage{}, ErrForbidden
	}
	memberUUID, err := parseUUID(memberID)
	if err != nil {
		return LogsPage{}, ErrNotFound
	}
	app, err := s.store.GetActiveAppByOwner(ctx, memberUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return LogsPage{Scope: "member", ScopeID: memberID, Items: []newapi.LogEntry{}, UpdatedAt: time.Now()}, nil
		}
		return LogsPage{}, fmt.Errorf("查询成员应用失败: %w", err)
	}
	keyID := parseInt64Default(app.NewapiKeyID.String, 0)
	if keyID == 0 || s.client == nil {
		return LogsPage{Scope: "member", ScopeID: memberID, Items: []newapi.LogEntry{}, UpdatedAt: time.Now()}, nil
	}
	page, err := s.client.GetTokenLogs(ctx, newapi.LogsQuery{
		TokenID:   keyID,
		Since:     opts.Since,
		Until:     opts.Until,
		Page:      opts.Page,
		PageSize:  opts.PageSize,
		ModelName: opts.ModelName,
	})
	if err != nil {
		if s.failAuditor != nil {
			s.failAuditor.RecordNewAPIFailure(ctx, NewAPIFailureContext{
				ActorID:   principal.UserID,
				ActorRole: principal.Role,
				OrgID:     orgID,
				Endpoint:  "GET /api/log/?token_id=...",
				Err:       err,
			})
		}
		return LogsPage{}, mapUsageError(err)
	}
	return LogsPage{Scope: "member", ScopeID: memberID, Items: page.Items, Total: page.Total, UpdatedAt: time.Now()}, nil
}

// GetOrgUsage 拉组织的按日 quota（透传 GET /api/data/users?id=<newapi_user_id>）。
func (s *UsageService) GetOrgUsage(ctx context.Context, principal auth.Principal, orgID string, since, until int64) (QuotaSeries, error) {
	if s.store == nil || s.client == nil {
		return QuotaSeries{}, ErrUsageUnavailable
	}
	if principal.Role != domain.UserRolePlatformAdmin && principal.OrgID != orgID {
		return QuotaSeries{}, ErrForbidden
	}
	// 组织级聚合用量不向普通成员开放，普通成员只能看自己应用维度。
	if principal.Role == domain.UserRoleOrgMember {
		return QuotaSeries{}, ErrForbidden
	}
	id, err := parseUUID(orgID)
	if err != nil {
		return QuotaSeries{}, ErrNotFound
	}
	org, err := s.store.GetOrganization(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return QuotaSeries{}, ErrNotFound
	}
	if err != nil {
		return QuotaSeries{}, fmt.Errorf("查询组织失败: %w", err)
	}
	if !org.NewapiUserID.Valid || org.NewapiUserID.String == "" {
		return QuotaSeries{Scope: "organization", ScopeID: orgID, Items: []newapi.QuotaDate{}, UpdatedAt: time.Now()}, nil
	}
	userID := parseInt64Default(org.NewapiUserID.String, 0)
	if userID == 0 {
		return QuotaSeries{Scope: "organization", ScopeID: orgID, Items: []newapi.QuotaDate{}, UpdatedAt: time.Now()}, nil
	}
	items, err := s.client.GetUserQuotaDates(ctx, userID, since, until)
	if err != nil {
		if s.failAuditor != nil {
			s.failAuditor.RecordNewAPIFailure(ctx, NewAPIFailureContext{
				ActorID:   principal.UserID,
				ActorRole: principal.Role,
				OrgID:     orgID,
				Endpoint:  "GET /api/data/users?id=...",
				Err:       err,
			})
		}
		return QuotaSeries{}, mapUsageError(err)
	}
	return QuotaSeries{Scope: "organization", ScopeID: orgID, Items: items, UpdatedAt: time.Now()}, nil
}

// GetPlatformUsage 拉全平台的按日 quota（透传 GET /api/data/）。仅平台管理员可调。
func (s *UsageService) GetPlatformUsage(ctx context.Context, principal auth.Principal, since, until int64) (QuotaSeries, error) {
	if s.client == nil {
		return QuotaSeries{}, ErrUsageUnavailable
	}
	if principal.Role != domain.UserRolePlatformAdmin {
		return QuotaSeries{}, ErrForbidden
	}
	items, err := s.client.GetAllQuotaDates(ctx, since, until)
	if err != nil {
		if s.failAuditor != nil {
			s.failAuditor.RecordNewAPIFailure(ctx, NewAPIFailureContext{
				ActorID:   principal.UserID,
				ActorRole: principal.Role,
				OrgID:     "",
				Endpoint:  "GET /api/data/",
				Err:       err,
			})
		}
		return QuotaSeries{}, mapUsageError(err)
	}
	return QuotaSeries{Scope: "platform", Items: items, UpdatedAt: time.Now()}, nil
}

// mapUsageError 把 newapi sentinel error 转成 service 层错误，避免暴露上游具体形态。
func mapUsageError(err error) error {
	if errors.Is(err, newapi.ErrUnauthorized) {
		return ErrUsageUnavailable
	}
	if errors.Is(err, newapi.ErrNotFound) {
		return ErrNotFound
	}
	return fmt.Errorf("查询用量失败: %w", err)
}

// parseInt64Default 把字符串转 int64，失败返回 fallback。
func parseInt64Default(s string, fallback int64) int64 {
	if s == "" {
		return fallback
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fallback
	}
	return v
}
