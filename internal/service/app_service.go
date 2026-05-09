package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// AppStore 抽象 app 服务的数据访问能力。
type AppStore interface {
	CreateApp(ctx context.Context, arg sqlc.CreateAppParams) (sqlc.App, error)
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	GetActiveAppByOwner(ctx context.Context, ownerUserID pgtype.UUID) (sqlc.App, error)
	ListAppsByOrg(ctx context.Context, arg sqlc.ListAppsByOrgParams) ([]sqlc.App, error)
	SetAppStatus(ctx context.Context, arg sqlc.SetAppStatusParams) (sqlc.App, error)
	SoftDeleteApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
}

// AppService 维护应用的查询和状态读取。
// 创建应用必须经过 onboarding 事务，因为应用的初始化需要联动 channel binding、audit、job。
type AppService struct {
	store AppStore
}

// NewAppService 创建 app 服务。
func NewAppService(store AppStore) *AppService { return &AppService{store: store} }

// AppResult 是对外的应用视图。
type AppResult struct {
	ID            string `json:"id"`
	OrgID         string `json:"org_id"`
	OwnerUserID   string `json:"owner_user_id"`
	RuntimeNodeID string `json:"runtime_node_id,omitempty"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	Status        string `json:"status"`
	PersonaMode   string `json:"persona_mode"`
	AppPrompt     string `json:"app_prompt,omitempty"`
	ContainerID   string `json:"container_id,omitempty"`
	APIKeyStatus  string `json:"api_key_status"`
	// NewapiKeyID 是 new-api 中 token 的数值 id；schema 上是 text 列存的字符串，
	// 这里解析成 int64 方便 usage service 直接调 GetAPIKey。0 表示未绑定。
	NewapiKeyID int64 `json:"newapi_key_id,omitempty"`
}

// Get 查询应用。
func (s *AppService) Get(ctx context.Context, principal auth.Principal, appID string) (AppResult, error) {
	id, err := parseUUID(appID)
	if err != nil {
		return AppResult{}, ErrNotFound
	}
	app, err := s.store.GetApp(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppResult{}, ErrNotFound
	}
	if err != nil {
		return AppResult{}, fmt.Errorf("查询应用失败: %w", err)
	}
	if !auth.CanViewApp(principal, uuidToString(app.OrgID), uuidToString(app.OwnerUserID)) {
		return AppResult{}, ErrForbidden
	}
	return toAppResult(app), nil
}

// ListByOrg 列出组织内的应用。
func (s *AppService) ListByOrg(ctx context.Context, principal auth.Principal, orgID string, limit, offset int32) ([]AppResult, error) {
	if !auth.CanViewOrg(principal, orgID) {
		return nil, ErrForbidden
	}
	id, err := parseUUID(orgID)
	if err != nil {
		return nil, ErrNotFound
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	apps, err := s.store.ListAppsByOrg(ctx, sqlc.ListAppsByOrgParams{OrgID: id, Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("查询应用列表失败: %w", err)
	}
	results := make([]AppResult, 0, len(apps))
	for _, app := range apps {
		// 组织成员只能在列表中看到自己拥有的应用。
		// schema 上每个用户最多一个活跃应用，分页含义对该角色无影响。
		if principal.Role == domain.UserRoleOrgMember && principal.UserID != uuidToString(app.OwnerUserID) {
			continue
		}
		results = append(results, toAppResult(app))
	}
	return results, nil
}

func canViewApp(principal auth.Principal, app sqlc.App) bool {
	switch principal.Role {
	case domain.UserRolePlatformAdmin:
		return true
	case domain.UserRoleOrgAdmin:
		return principal.OrgID == uuidToString(app.OrgID)
	case domain.UserRoleOrgMember:
		return principal.UserID == uuidToString(app.OwnerUserID)
	default:
		return false
	}
}

func toAppResult(app sqlc.App) AppResult {
	result := AppResult{
		ID:           uuidToString(app.ID),
		OrgID:        uuidToString(app.OrgID),
		OwnerUserID:  uuidToString(app.OwnerUserID),
		Name:         app.Name,
		Status:       app.Status,
		PersonaMode:  app.PersonaMode,
		APIKeyStatus: app.ApiKeyStatus,
	}
	if app.RuntimeNodeID.Valid {
		result.RuntimeNodeID = uuidToOptionalString(app.RuntimeNodeID)
	}
	if app.Description.Valid {
		result.Description = app.Description.String
	}
	if app.AppPrompt.Valid {
		result.AppPrompt = app.AppPrompt.String
	}
	if app.ContainerID.Valid {
		result.ContainerID = app.ContainerID.String
	}
	if app.NewapiKeyID.Valid {
		// schema 上 newapi_key_id 是 text，但 manager 写入的恒是 int64 字符串。
		// 解析失败一律视为未绑定，避免污染 service 层。
		if id, err := strconv.ParseInt(app.NewapiKeyID.String, 10, 64); err == nil {
			result.NewapiKeyID = id
		}
	}
	return result
}
