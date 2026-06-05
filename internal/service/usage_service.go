package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/store/sqlc"
)

// UsageNewAPIClient 是 manager 透传 new-api 用量端点所需的最小集合。
//
// manager 端不再对用量数据做缓存或多维聚合：每个接口直接代理对应的 new-api endpoint，
// 让 new-api 维护一切按 token / user / 平台的 quota 统计与分组。
type UsageNewAPIClient interface {
	GetTokenLogs(ctx context.Context, q newapi.LogsQuery) (newapi.LogsPage, error)
	// GetUserQuotaDates 需要传 username：new-api 端 id 参数被静默忽略，
	// client 必须按 username 做客户端过滤，否则会拿到全平台所有用户的聚合数据。
	GetUserQuotaDates(ctx context.Context, userID int64, username string, since, until int64) ([]newapi.QuotaDate, error)
	GetAllQuotaDates(ctx context.Context, since, until int64) ([]newapi.QuotaDate, error)
}

// UsageStore 是 service 把 manager UUID 转 new-api 数字 id 用到的最小数据访问能力。
type UsageStore interface {
	GetApp(ctx context.Context, id string) (sqlc.App, error)
	GetActiveAppByOwner(ctx context.Context, ownerUserID string) (sqlc.App, error)
	GetOrganization(ctx context.Context, id string) (sqlc.Organization, error)
	// ListAllActiveOrganizations 全量返回未软删除的组织，供 GetOrgUsageBreakdown 批量查询用量。
	ListAllActiveOrganizations(ctx context.Context) ([]sqlc.Organization, error)
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
	// Scope 标识日志所属维度，当前为 app 或 member。
	Scope string `json:"scope"`
	// ScopeID 是对应维度的 manager UUID，便于前端复核查询上下文。
	ScopeID string `json:"scope_id,omitempty"`
	// Items 透传 new-api LogEntry 列表。
	Items []newapi.LogEntry `json:"items" swaggerignore:"true"`
	// Total 是 new-api 返回的分页总数。
	Total int `json:"total"`
	// UpdatedAt 是 manager 代理完成时刻，不代表 new-api 内部采集时间。
	UpdatedAt time.Time `json:"updated_at"`
}

// QuotaSeries 是 organization / platform 维度的响应：透传 new-api 的按日 quota 汇总。
type QuotaSeries struct {
	// Scope 标识配额序列所属维度，当前为 organization 或 platform。
	Scope string `json:"scope"`
	// ScopeID 是企业维度的 manager org UUID；平台维度为空。
	ScopeID string `json:"scope_id,omitempty"`
	// Items 透传 new-api QuotaDate 列表。
	Items []newapi.QuotaDate `json:"items" swaggerignore:"true"`
	// UpdatedAt 是 manager 代理完成时刻，不代表 new-api 内部采集时间。
	UpdatedAt time.Time `json:"updated_at"`
}

// OrgUsageItem 是单个组织在指定时间窗内的 quota 消耗汇总。
type OrgUsageItem struct {
	// OrgID 是企业 UUID。
	OrgID string `json:"org_id"`
	// OrgName 是企业显示名。
	OrgName string `json:"org_name"`
	// TotalQuota 是 [since, until] 内各日 QuotaDate.Quota 的累加值。
	TotalQuota int64 `json:"total_quota"`
}

// OrgUsageBreakdown 是 GET /api/v1/platform/usage/org-breakdown 的响应视图。
type OrgUsageBreakdown struct {
	// Items 按 TotalQuota 降序排列，最多 10 条。
	Items []OrgUsageItem `json:"items"`
	// UpdatedAt 是 manager 完成聚合的时刻。
	UpdatedAt time.Time `json:"updated_at"`
}

// LogsQueryOptions 是对外暴露的查询选项；service 不内置默认时间窗（避免和前端分页错位），
// 但 PageSize 缺省 20 与 newapi.LogsQuery 保持一致。
type LogsQueryOptions struct {
	// Since 是查询起始 Unix 秒；0 表示不限制起始时间。
	Since int64
	// Until 是查询截止 Unix 秒；0 表示不限制截止时间。
	Until int64
	// Page 是 new-api 日志分页页码。
	Page int
	// PageSize 是 new-api 日志分页大小，0 时 handler/service 使用默认值。
	PageSize int
	// ModelName 是可选模型名过滤条件。
	ModelName string
}

// GetAppUsage 拉指定应用 token 的调用日志（透传 GET /api/log/?token_name=X）。
//
// 鉴权必须基于数据库中应用的真实归属（org_id / owner_user_id），不能信任调用方传入的
// owner 参数：否则组织成员只要把 owner_user_id 伪造成自己，再带上任意 appID，就能借
// CanViewApp 的 member 分支（p.UserID == appOwnerUserID）通过校验、读取他人应用的用量
// （横向越权 / IDOR）。因此这里先按 appID 取出应用，再用其真实归属做权限判定。
func (s *UsageService) GetAppUsage(ctx context.Context, principal auth.Principal, appID string, newapiKeyID int64, opts LogsQueryOptions) (LogsPage, error) {
	// 缺少 new-api client 或 store 时用量功能整体不可用，无关具体应用，提前返回。
	if s.client == nil || s.store == nil {
		return LogsPage{}, ErrUsageUnavailable
	}
	// appID 直接作为字符串传入，不再需要解析为 UUID 类型。
	// 应用不存在时返回 ErrNotFound（404）；无法读到归属就无法安全鉴权，绝不放行。
	app, err := s.store.GetApp(ctx, appID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return LogsPage{}, ErrNotFound
		}
		return LogsPage{}, ErrUsageUnavailable
	}
	if !auth.CanReadAppKnowledge(principal, app.OrgID, app.OwnerUserID) {
		return LogsPage{}, ErrForbidden
	}
	if newapiKeyID == 0 {
		return LogsPage{Scope: "app", ScopeID: appID, Items: []newapi.LogEntry{}, UpdatedAt: time.Now()}, nil
	}
	// new-api admin /api/log/?token_id= 被实测静默忽略，必须用 token_name 过滤。
	// 优先读 apps.newapi_key_name；字段为空（历史未回填数据）时回退到约定 "app-"+appID，
	// 该值与 app_initialize 注册 token 时使用的名字保持一致。
	keyName := "app-" + appID
	if app.NewapiKeyName.Valid && app.NewapiKeyName.String != "" {
		keyName = app.NewapiKeyName.String
	}
	page, err := s.client.GetTokenLogs(ctx, newapi.LogsQuery{
		TokenName: keyName,
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
				OrgID:     app.OrgID,
				Endpoint:  "GET /api/log/?token_name=...",
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
	if !auth.CanViewMemberUsage(principal, orgID, memberID) {
		return LogsPage{}, ErrForbidden
	}
	// memberID 直接作为字符串传入。
	app, err := s.store.GetActiveAppByOwner(ctx, memberID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return LogsPage{Scope: "member", ScopeID: memberID, Items: []newapi.LogEntry{}, UpdatedAt: time.Now()}, nil
		}
		return LogsPage{}, fmt.Errorf("查询成员应用失败: %w", err)
	}
	keyID := parseInt64Default(app.NewapiKeyID.String, 0)
	if keyID == 0 || s.client == nil {
		return LogsPage{Scope: "member", ScopeID: memberID, Items: []newapi.LogEntry{}, UpdatedAt: time.Now()}, nil
	}
	// new-api admin /api/log/?token_id= 被实测静默忽略，必须用 token_name 过滤。
	// 优先读 apps.newapi_key_name；字段空（历史 / 未回填数据）时回退到 "app-"+app.ID。
	keyName := app.NewapiKeyName.String
	if keyName == "" {
		// app.ID 已是 string，直接使用。
		keyName = "app-" + app.ID
	}
	page, err := s.client.GetTokenLogs(ctx, newapi.LogsQuery{
		TokenName: keyName,
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
				Endpoint:  "GET /api/log/?token_name=...",
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
	if !auth.CanViewOrgUsage(principal, orgID) {
		return QuotaSeries{}, ErrForbidden
	}
	// orgID 直接作为字符串传入。
	org, err := s.store.GetOrganization(ctx, orgID)
	if errors.Is(err, sql.ErrNoRows) {
		return QuotaSeries{}, ErrNotFound
	}
	if err != nil {
		return QuotaSeries{}, fmt.Errorf("查询企业失败: %w", err)
	}
	if !org.NewapiUserID.Valid || org.NewapiUserID.String == "" {
		return QuotaSeries{Scope: "organization", ScopeID: orgID, Items: []newapi.QuotaDate{}, UpdatedAt: time.Now()}, nil
	}
	userID := parseInt64Default(org.NewapiUserID.String, 0)
	if userID == 0 {
		return QuotaSeries{Scope: "organization", ScopeID: orgID, Items: []newapi.QuotaDate{}, UpdatedAt: time.Now()}, nil
	}
	// org.NewapiUsername 是 client 做客户端过滤 /api/data/users 响应的依据；
	// 字段空意味着老数据未回填，此时不调 newapi，直接返回空 series 避免污染
	// （new-api 响应里固定包含全平台所有用户的聚合，没有 username 无法精确过滤）。
	if !org.NewapiUsername.Valid || org.NewapiUsername.String == "" {
		return QuotaSeries{Scope: "organization", ScopeID: orgID, Items: []newapi.QuotaDate{}, UpdatedAt: time.Now()}, nil
	}
	items, err := s.client.GetUserQuotaDates(ctx, userID, org.NewapiUsername.String, since, until)
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

// GetOrgUsageBreakdown 聚合全平台各组织在 [since, until] 内的 quota 消耗，
// 按消耗量降序返回 top 10。仅 platform_admin 可调。
//
// 并发上限 5：避免对 new-api 产生瞬时大批请求；无 newapi 账号的组织跳过。
func (s *UsageService) GetOrgUsageBreakdown(ctx context.Context, principal auth.Principal, since, until int64) (OrgUsageBreakdown, error) {
	if s.client == nil {
		return OrgUsageBreakdown{}, ErrUsageUnavailable
	}
	if !auth.CanViewPlatformUsage(principal) {
		return OrgUsageBreakdown{}, ErrForbidden
	}

	orgs, err := s.store.ListAllActiveOrganizations(ctx)
	if err != nil {
		return OrgUsageBreakdown{}, fmt.Errorf("查询企业列表失败: %w", err)
	}

	// 并发收集各组织用量；mu 保护 items 切片。
	var mu sync.Mutex
	var items []OrgUsageItem

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(5) // 并发上限，避免 new-api 过载
	for _, org := range orgs {
		// 跳过没有 new-api 账号或 username 的组织（历史数据 / 尚未初始化）。
		if !org.NewapiUserID.Valid || org.NewapiUserID.String == "" ||
			!org.NewapiUsername.Valid || org.NewapiUsername.String == "" {
			continue
		}
		org := org
		g.Go(func() error {
			userID := parseInt64Default(org.NewapiUserID.String, 0)
			if userID == 0 {
				return nil
			}
			dates, err := s.client.GetUserQuotaDates(gctx, userID, org.NewapiUsername.String, since, until)
			if err != nil {
				// 记录 new-api 调用失败，供监控统计使用。
				if s.failAuditor != nil {
					s.failAuditor.RecordNewAPIFailure(gctx, NewAPIFailureContext{
						ActorID:   principal.UserID,
						ActorRole: principal.Role,
						OrgID:     org.ID,
						Endpoint:  "GET /api/data/users?id=...",
						Err:       err,
					})
				}
				return fmt.Errorf("查询企业 %s 用量失败: %w", org.ID, err)
			}
			var total int64
			for _, d := range dates {
				total += d.Quota
			}
			mu.Lock()
			items = append(items, OrgUsageItem{
				OrgID:      org.ID,
				OrgName:    org.Name,
				TotalQuota: total,
			})
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return OrgUsageBreakdown{}, mapUsageError(err)
	}

	// 降序排列，截取前 10 条。
	sort.Slice(items, func(i, j int) bool {
		return items[i].TotalQuota > items[j].TotalQuota
	})
	if len(items) > 10 {
		items = items[:10]
	}
	return OrgUsageBreakdown{Items: items, UpdatedAt: time.Now()}, nil
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
