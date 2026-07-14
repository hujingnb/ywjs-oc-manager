package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	null "github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/store/sqlc"
)

// TestUsageServiceForbidsCrossOrg 校验组织管理员只能看自己 org 的应用用量：
// 应用真实归属 org 与主体 org 不一致时按数据库归属拒绝（不再信任调用方传入的 owner）。
func TestUsageServiceForbidsCrossOrg(t *testing.T) {
	store := &fakeUsageStore{appByID: map[string]sqlc.App{
		"app-1": {ID: "app-1", OrgID: "org-1", OwnerUserID: "owner-user"},
	}}
	svc := NewUsageService(store, &fakeUsageClient{}, nil)
	_, err := svc.GetAppUsage(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "other"}, "app-1", 1, LogsQueryOptions{})
	require.ErrorIs(t, err, ErrForbidden)
}

// TestUsageServiceAppProxiesTokenLogs 校验 GetAppUsage 直接调 GetTokenLogs(token_id=…)
// 并把返回 items 透传到 LogsPage.Items。
func TestUsageServiceAppProxiesTokenLogs(t *testing.T) {
	client := &fakeUsageClient{tokenLogs: newapi.LogsPage{
		Items: []newapi.LogEntry{{ID: 1, ModelName: "qwen2.5", Quota: 100}},
		Total: 1,
	}}
	// 应用存在但 NewapiKeyName 未回填，service 应回退到约定 "app-"+appID 作 token_name。
	store := &fakeUsageStore{appByID: map[string]sqlc.App{
		"app-1": {ID: "app-1", OrgID: "org-1", OwnerUserID: "owner-user"},
	}}
	svc := NewUsageService(store, client, nil)

	view, err := svc.GetAppUsage(context.Background(), platformAdmin(), "app-1", 42, LogsQueryOptions{Page: 1, PageSize: 50})
	require.NoError(t, err)
	if client.lastTokenLogsQuery.TokenName != "app-app-1" || client.lastTokenLogsQuery.PageSize != 50 {
		t.Fatalf("token logs query = %+v", client.lastTokenLogsQuery)
	}
	if len(view.Items) != 1 || view.Items[0].Quota != 100 {
		t.Fatalf("view = %+v", view)
	}
}

// TestUsageServiceAppMapsErrors 校验 newapi.ErrNotFound / ErrUnauthorized 的映射。
// 注入真实 app 行使流程越过鉴权后再触发 client 错误，从而校验错误映射本身。
func TestUsageServiceAppMapsErrors(t *testing.T) {
	appStore := func() *fakeUsageStore {
		return &fakeUsageStore{appByID: map[string]sqlc.App{
			"app": {ID: "app", OrgID: "org-1", OwnerUserID: "owner-user", NewapiKeyName: null.StringFrom("app-token")},
		}}
	}
	svc := NewUsageService(appStore(), &fakeUsageClient{tokenLogsError: newapi.ErrNotFound}, nil)
	if _, err := svc.GetAppUsage(context.Background(), platformAdmin(), "app", 1, LogsQueryOptions{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}

	svc = NewUsageService(appStore(), &fakeUsageClient{tokenLogsError: newapi.ErrUnauthorized}, nil)
	if _, err := svc.GetAppUsage(context.Background(), platformAdmin(), "app", 1, LogsQueryOptions{}); !errors.Is(err, ErrUsageUnavailable) {
		t.Fatalf("error = %v, want ErrUsageUnavailable", err)
	}
}

// TestUsageServiceAppZeroKeyReturnsEmpty 校验 newapiKeyID=0（应用尚未初始化）时直接返回空 LogsPage。
func TestUsageServiceAppZeroKeyReturnsEmpty(t *testing.T) {
	client := &fakeUsageClient{}
	store := &fakeUsageStore{appByID: map[string]sqlc.App{
		"app": {ID: "app", OrgID: "org-1", OwnerUserID: "owner-user"},
	}}
	svc := NewUsageService(store, client, nil)
	view, err := svc.GetAppUsage(context.Background(), platformAdmin(), "app", 0, LogsQueryOptions{})
	require.NoError(t, err)
	require.Equal(t, 0, len(view.Items))
	require.Equal(t, 0, client.tokenLogsCalls)
}

// TestUsageServiceAppRejectsAICCHiddenApp 覆盖普通 app 用量入口隔离：AICC 隐藏 app
// 不允许通过普通应用用量接口读取 new-api token 日志。
func TestUsageServiceAppRejectsAICCHiddenApp(t *testing.T) {
	client := &fakeUsageClient{}
	store := &fakeUsageStore{appByID: map[string]sqlc.App{
		"app": {ID: "app", OrgID: "org-1", OwnerUserID: "owner-user", AppType: string(domain.AppTypeAICC), NewapiKeyName: null.StringFrom("app-token")},
	}}
	svc := NewUsageService(store, client, nil)

	_, err := svc.GetAppUsage(context.Background(), platformAdmin(), "app", 1, LogsQueryOptions{})

	require.ErrorIs(t, err, ErrNotFound)
	require.Equal(t, 0, client.tokenLogsCalls)
}

// TestUsageServiceMissingClient 校验 client=nil 时 ErrUsageUnavailable。
func TestUsageServiceMissingClient(t *testing.T) {
	svc := NewUsageService(&fakeUsageStore{}, nil, nil)
	if _, err := svc.GetAppUsage(context.Background(), platformAdmin(), "app", 1, LogsQueryOptions{}); !errors.Is(err, ErrUsageUnavailable) {
		t.Fatalf("error = %v, want ErrUsageUnavailable", err)
	}
}

// TestUsageServiceMemberUsesActiveAppByOwner 校验 member 维度通过 GetActiveAppByOwner
// 拿到 app.newapi_key_name 再调 GetTokenLogs。
func TestUsageServiceMemberUsesActiveAppByOwner(t *testing.T) {
	memberID := mustUUID(t, "00000000-0000-0000-0000-000000000c01")
	appID := mustUUID(t, "0193ce63-4b8e-7000-a000-000000000100")
	store := &fakeUsageStore{
		activeApp: sqlc.App{
			ID:            appID,
			NewapiKeyID:   null.StringFrom("77"),
			NewapiKeyName: null.StringFrom("app-fixed-uuid-for-test"),
		},
	}
	client := &fakeUsageClient{tokenLogs: newapi.LogsPage{Items: []newapi.LogEntry{{ID: 9}}, Total: 1}}
	svc := NewUsageService(store, client, nil)
	view, err := svc.GetMemberUsage(context.Background(), platformAdmin(), "00000000-0000-0000-0000-000000000b01", "00000000-0000-0000-0000-000000000c01", LogsQueryOptions{})
	require.NoError(t, err)
	if !store.activeAppCalled || store.lastActiveOwner != memberID {
		t.Fatalf("expected GetActiveAppByOwner, got=%v owner=%v", store.activeAppCalled, store.lastActiveOwner)
	}
	// service 应优先用 app.NewapiKeyName 作 token_name 过滤口径。
	if client.lastTokenLogsQuery.TokenName != "app-fixed-uuid-for-test" || len(view.Items) != 1 {
		t.Fatalf("view = %+v query = %+v", view, client.lastTokenLogsQuery)
	}
}

// TestUsageServiceMemberAllowsOrgMemberSelfOnly 验证用量服务成员允许组织成员自身仅的预期行为场景。
func TestUsageServiceMemberAllowsOrgMemberSelfOnly(t *testing.T) {
	orgID := "00000000-0000-0000-0000-000000000b01"
	memberID := "00000000-0000-0000-0000-000000000c01"
	appID := mustUUID(t, "0193ce63-4b8e-7000-a000-000000000200")
	store := &fakeUsageStore{
		activeApp: sqlc.App{
			ID:            appID,
			NewapiKeyID:   null.StringFrom("77"),
			NewapiKeyName: null.StringFrom("app-self-token"),
		},
	}
	client := &fakeUsageClient{tokenLogs: newapi.LogsPage{Items: []newapi.LogEntry{{ID: 9}}, Total: 1}}
	svc := NewUsageService(store, client, nil)

	view, err := svc.GetMemberUsage(context.Background(), auth.Principal{
		Role:   domain.UserRoleOrgMember,
		OrgID:  orgID,
		UserID: memberID,
	}, orgID, memberID, LogsQueryOptions{})
	require.NoError(t, err)
	require.Len(t, view.Items, 1)
	require.Equal(t, "app-self-token", client.lastTokenLogsQuery.TokenName)
}

// TestUsageServiceMemberRejectsOrgMemberOtherUsage 验证用量服务成员拒绝组织成员其他用量的异常或拒绝路径场景。
func TestUsageServiceMemberRejectsOrgMemberOtherUsage(t *testing.T) {
	orgID := "00000000-0000-0000-0000-000000000b01"
	svc := NewUsageService(&fakeUsageStore{}, &fakeUsageClient{}, nil)

	_, err := svc.GetMemberUsage(context.Background(), auth.Principal{
		Role:   domain.UserRoleOrgMember,
		OrgID:  orgID,
		UserID: "00000000-0000-0000-0000-000000000c02",
	}, orgID, "00000000-0000-0000-0000-000000000c01", LogsQueryOptions{})
	require.ErrorIs(t, err, ErrForbidden)
}

// TestUsageServiceOrgUsesNewapiUserID 校验 org 维度查 organizations.newapi_user_id 后调 GetUserQuotaDates。
func TestUsageServiceOrgUsesNewapiUserID(t *testing.T) {
	orgUUID := mustUUID(t, "00000000-0000-0000-0000-000000000b01")
	store := &fakeUsageStore{
		org: sqlc.Organization{
			ID:             orgUUID,
			NewapiUserID:   null.StringFrom("55"),
			NewapiUsername: null.StringFrom("org-55-user"),
		},
	}
	client := &fakeUsageClient{userQuota: []newapi.QuotaDate{{Date: "2026-05-01", Quota: 100}}}
	svc := NewUsageService(store, client, nil)
	view, err := svc.GetOrgUsage(context.Background(), platformAdmin(), "00000000-0000-0000-0000-000000000b01", 0, 0)
	require.NoError(t, err)
	require.Equal(t, int64(55), client.lastUserQuotaUserID)
	require.Equal(t, "org-55-user", client.lastUserQuotaUsername)
	if len(view.Items) != 1 || view.Items[0].Quota != 100 {
		t.Fatalf("view = %+v", view)
	}
}

// TestUsageServicePlatformOnlyAdmin 校验非 platform_admin 拿不到 platform 维度。
func TestUsageServicePlatformOnlyAdmin(t *testing.T) {
	svc := NewUsageService(&fakeUsageStore{}, &fakeUsageClient{}, nil)
	_, err := svc.GetPlatformUsage(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin}, 0, 0)
	require.ErrorIs(t, err, ErrForbidden)
}

// TestUsageServiceOrgRejectsOrgMember 验证用量服务组织拒绝组织成员的异常或拒绝路径场景。
func TestUsageServiceOrgRejectsOrgMember(t *testing.T) {
	orgID := "00000000-0000-0000-0000-000000000b01"
	svc := NewUsageService(&fakeUsageStore{}, &fakeUsageClient{}, nil)

	_, err := svc.GetOrgUsage(context.Background(), auth.Principal{
		Role:   domain.UserRoleOrgMember,
		OrgID:  orgID,
		UserID: "00000000-0000-0000-0000-000000000c01",
	}, orgID, 0, 0)
	require.ErrorIs(t, err, ErrForbidden)
}

// TestUsageServicePlatformProxiesAllQuotaDates 校验 platform 维度直接调 GetAllQuotaDates。
func TestUsageServicePlatformProxiesAllQuotaDates(t *testing.T) {
	client := &fakeUsageClient{allQuota: []newapi.QuotaDate{{Date: "2026-05-01", Quota: 100}}}
	svc := NewUsageService(&fakeUsageStore{}, client, nil)
	view, err := svc.GetPlatformUsage(context.Background(), platformAdmin(), 0, 0)
	require.NoError(t, err)
	if !client.allQuotaCalled || view.Scope != "platform" || len(view.Items) != 1 {
		t.Fatalf("view = %+v called=%v", view, client.allQuotaCalled)
	}
}

// TestUsageService_AppUsageFailureRecordsAudit 校验 GetAppUsage new-api 调用失败时触发审计。
func TestUsageService_AppUsageFailureRecordsAudit(t *testing.T) {
	auditor := &fakeFailAuditor{}
	client := &fakeUsageClient{tokenLogsError: errors.New("5xx")}
	// 审计的 OrgID 取自应用真实归属（app.OrgID），不再来自调用方入参。
	store := &fakeUsageStore{appByID: map[string]sqlc.App{
		"app-1": {ID: "app-1", OrgID: "owner-org", OwnerUserID: "owner-user", NewapiKeyName: null.StringFrom("app-token")},
	}}
	svc := NewUsageService(store, client, auditor)
	_, err := svc.GetAppUsage(context.Background(), platformAdmin(), "app-1", 42, LogsQueryOptions{})
	require.Error(t, err)
	require.Equal(t, 1, len(auditor.events))
	ev := auditor.events[0]
	require.Equal(t, "owner-org", ev.OrgID)
	require.Equal(t, "GET /api/log/?token_name=...", ev.Endpoint)
}

// TestUsageService_MemberUsageFailureRecordsAudit 校验 GetMemberUsage new-api 调用失败时触发审计。
func TestUsageService_MemberUsageFailureRecordsAudit(t *testing.T) {
	auditor := &fakeFailAuditor{}
	store := &fakeUsageStore{
		activeApp: sqlc.App{
			NewapiKeyID: null.StringFrom("77"),
		},
	}
	client := &fakeUsageClient{tokenLogsError: errors.New("5xx")}
	svc := NewUsageService(store, client, auditor)
	orgID := "00000000-0000-0000-0000-000000000b01"
	memberID := "00000000-0000-0000-0000-000000000c01"
	_, err := svc.GetMemberUsage(context.Background(), platformAdmin(), orgID, memberID, LogsQueryOptions{})
	require.Error(t, err)
	require.Equal(t, 1, len(auditor.events))
	ev := auditor.events[0]
	require.Equal(t, orgID, ev.OrgID)
	require.Equal(t, "GET /api/log/?token_name=...", ev.Endpoint)
}

// TestUsageService_OrgUsageFailureRecordsAudit 校验 GetOrgUsage new-api 调用失败时触发审计。
func TestUsageService_OrgUsageFailureRecordsAudit(t *testing.T) {
	auditor := &fakeFailAuditor{}
	orgID := "00000000-0000-0000-0000-000000000b01"
	orgUUID := mustUUID(t, orgID)
	store := &fakeUsageStore{
		org: sqlc.Organization{
			ID:             orgUUID,
			NewapiUserID:   null.StringFrom("55"),
			NewapiUsername: null.StringFrom("org-55-user"),
		},
	}
	client := &fakeUsageClient{userQuotaError: errors.New("5xx")}
	svc := NewUsageService(store, client, auditor)
	_, err := svc.GetOrgUsage(context.Background(), platformAdmin(), orgID, 0, 0)
	require.Error(t, err)
	require.Equal(t, 1, len(auditor.events))
	ev := auditor.events[0]
	require.Equal(t, orgID, ev.OrgID)
	require.Equal(t, "GET /api/data/users?id=...", ev.Endpoint)
}

// TestUsageService_PlatformUsageFailureRecordsAudit 校验 GetPlatformUsage new-api 调用失败时触发审计。
func TestUsageService_PlatformUsageFailureRecordsAudit(t *testing.T) {
	auditor := &fakeFailAuditor{}
	client := &fakeUsageClient{allQuotaError: errors.New("5xx")}
	svc := NewUsageService(&fakeUsageStore{}, client, auditor)
	_, err := svc.GetPlatformUsage(context.Background(), platformAdmin(), 0, 0)
	require.Error(t, err)
	require.Equal(t, 1, len(auditor.events))
	ev := auditor.events[0]
	require.Equal(t, "", ev.OrgID)
	require.Equal(t, "GET /api/data/", ev.Endpoint)
}

// fakeUsageClient 实现 UsageNewAPIClient，供 service 单测注入。
type fakeUsageClient struct {
	tokenLogs             newapi.LogsPage
	tokenLogsError        error
	tokenLogsCalls        int
	lastTokenLogsQuery    newapi.LogsQuery
	userQuota             []newapi.QuotaDate
	userQuotaError        error
	lastUserQuotaUserID   int64
	lastUserQuotaUsername string
	allQuota              []newapi.QuotaDate
	allQuotaError         error
	allQuotaCalled        bool
}

func (c *fakeUsageClient) GetTokenLogs(_ context.Context, q newapi.LogsQuery) (newapi.LogsPage, error) {
	c.tokenLogsCalls++
	c.lastTokenLogsQuery = q
	if c.tokenLogsError != nil {
		return newapi.LogsPage{}, c.tokenLogsError
	}
	return c.tokenLogs, nil
}

func (c *fakeUsageClient) GetUserQuotaDates(_ context.Context, userID int64, username string, _, _ int64) ([]newapi.QuotaDate, error) {
	c.lastUserQuotaUserID = userID
	c.lastUserQuotaUsername = username
	if c.userQuotaError != nil {
		return nil, c.userQuotaError
	}
	return c.userQuota, nil
}

func (c *fakeUsageClient) GetAllQuotaDates(_ context.Context, _, _ int64) ([]newapi.QuotaDate, error) {
	c.allQuotaCalled = true
	if c.allQuotaError != nil {
		return nil, c.allQuotaError
	}
	return c.allQuota, nil
}

// fakeUsageStore 实现 UsageStore；activeApp / org 用作 lookup 返回值。
// appByID 用于按 ID 注入 GetApp 返回值，覆盖 service 优先读 app.NewapiKeyName
// 的 happy path；未注入时 GetApp 仍然返回 sql.ErrNoRows，保证现有走回退路径
// 的用例行为不变。
type fakeUsageStore struct {
	activeApp       sqlc.App
	activeAppCalled bool
	// lastActiveOwner 记录最近一次 GetActiveAppByOwner 调用传入的 ownerUserID（string）。
	lastActiveOwner string
	org             sqlc.Organization
	appByID         map[string]sqlc.App
	allActiveOrgs   []sqlc.Organization
}

func (s *fakeUsageStore) GetApp(_ context.Context, id string) (sqlc.App, error) {
	// 仅当用例显式注入 appByID 时才返回对应记录，缺省回到 sql.ErrNoRows
	// 以保持既有 fallback 用例（如 TestGetAppUsageFallsBackWhenKeyNameEmpty）的行为。
	if s.appByID != nil {
		if app, ok := s.appByID[id]; ok {
			return app, nil
		}
	}
	return sqlc.App{}, sql.ErrNoRows
}

func (s *fakeUsageStore) GetActiveAppByOwner(_ context.Context, ownerID string) (sqlc.App, error) {
	s.activeAppCalled = true
	s.lastActiveOwner = ownerID
	return s.activeApp, nil
}

func (s *fakeUsageStore) GetOrganization(_ context.Context, id string) (sqlc.Organization, error) {
	if s.org.ID != "" && s.org.ID == id {
		return s.org, nil
	}
	return sqlc.Organization{}, sql.ErrNoRows
}

// ListAllActiveOrganizations 是 allActiveOrgs 的预置返回值，供测试注入。
func (s *fakeUsageStore) ListAllActiveOrganizations(_ context.Context) ([]sqlc.Organization, error) {
	return s.allActiveOrgs, nil
}

// TestGetAppUsageFallsBackWhenKeyNameEmpty 校验 app 存在但 newapi_key_name 字段为空时，
// service 回退到 "app-"+appID 的拼装路径，确保历史/未回填数据仍然可查。
func TestGetAppUsageFallsBackWhenKeyNameEmpty(t *testing.T) {
	appID := "0193ce63-4b8e-7000-a000-000000000001"
	client := &fakeUsageClient{tokenLogs: newapi.LogsPage{Items: []newapi.LogEntry{{ID: 1}}, Total: 1}}
	// 故意不设 NewapiKeyName，模拟旧数据未回填的边界。
	store := &fakeUsageStore{appByID: map[string]sqlc.App{
		appID: {ID: appID, OrgID: "owner-org", OwnerUserID: "owner-user"},
	}}
	svc := NewUsageService(store, client, nil)

	_, err := svc.GetAppUsage(context.Background(), platformAdmin(),
		appID,
		42,
		LogsQueryOptions{Page: 1, PageSize: 50})
	require.NoError(t, err)
	assert.Equal(t, "app-0193ce63-4b8e-7000-a000-000000000001", client.lastTokenLogsQuery.TokenName)
	assert.Equal(t, 50, client.lastTokenLogsQuery.PageSize)
}

// TestGetMemberUsageUsesAppNewapiKeyName 校验 member 维度走 GetActiveAppByOwner
// 拿到 app 后用 app.NewapiKeyName 作 TokenName。
func TestGetMemberUsageUsesAppNewapiKeyName(t *testing.T) {
	appID := mustUUID(t, "0193ce63-4b8e-7000-a000-000000000002")
	store := &fakeUsageStore{
		activeApp: sqlc.App{
			ID:            appID,
			NewapiKeyID:   null.StringFrom("77"),
			NewapiKeyName: null.StringFrom("app-0193ce63-4b8e-7000-a000-000000000002"),
		},
	}
	client := &fakeUsageClient{tokenLogs: newapi.LogsPage{Items: []newapi.LogEntry{{ID: 9}}, Total: 1}}
	svc := NewUsageService(store, client, nil)

	_, err := svc.GetMemberUsage(context.Background(), platformAdmin(),
		"00000000-0000-0000-0000-000000000b01",
		"00000000-0000-0000-0000-000000000c01",
		LogsQueryOptions{Page: 1, PageSize: 20})
	require.NoError(t, err)
	assert.Equal(t, "app-0193ce63-4b8e-7000-a000-000000000002", client.lastTokenLogsQuery.TokenName)
}

// TestGetMemberUsageFallsBackWhenKeyNameEmpty 校验 app.NewapiKeyName 字段空时回退到拼串路径。
func TestGetMemberUsageFallsBackWhenKeyNameEmpty(t *testing.T) {
	appID := mustUUID(t, "0193ce63-4b8e-7000-a000-000000000003")
	store := &fakeUsageStore{
		// 故意不设 NewapiKeyName 以模拟旧数据未回填的边界。
		activeApp: sqlc.App{
			ID:          appID,
			NewapiKeyID: null.StringFrom("78"),
		},
	}
	client := &fakeUsageClient{tokenLogs: newapi.LogsPage{}}
	svc := NewUsageService(store, client, nil)

	_, err := svc.GetMemberUsage(context.Background(), platformAdmin(),
		"00000000-0000-0000-0000-000000000b01",
		"00000000-0000-0000-0000-000000000c01",
		LogsQueryOptions{})
	require.NoError(t, err)
	assert.Equal(t, "app-0193ce63-4b8e-7000-a000-000000000003", client.lastTokenLogsQuery.TokenName)
}

// TestGetOrgUsagePassesUsernameToClient 校验 GetOrgUsage 把 organizations.newapi_username 透传给 client。
func TestGetOrgUsagePassesUsernameToClient(t *testing.T) {
	orgID := mustUUID(t, "0193ce63-4b8e-7000-a000-0000000000aa")
	store := &fakeUsageStore{
		org: sqlc.Organization{
			ID:             orgID,
			NewapiUserID:   null.StringFrom("14"),
			NewapiUsername: null.StringFrom("m8-test"),
		},
	}
	client := &fakeUsageClient{userQuota: []newapi.QuotaDate{{Date: "2026-05-19", Quota: 7}}}
	svc := NewUsageService(store, client, nil)

	_, err := svc.GetOrgUsage(context.Background(), platformAdmin(), orgID, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(14), client.lastUserQuotaUserID)
	assert.Equal(t, "m8-test", client.lastUserQuotaUsername)
}

// TestGetOrgUsageReturnsEmptyWhenUsernameEmpty 校验 organizations.newapi_username 为空时返回空 series。
func TestGetOrgUsageReturnsEmptyWhenUsernameEmpty(t *testing.T) {
	orgID := mustUUID(t, "0193ce63-4b8e-7000-a000-0000000000bb")
	store := &fakeUsageStore{
		org: sqlc.Organization{
			ID:           orgID,
			NewapiUserID: null.StringFrom("99"),
			// NewapiUsername 故意留空，模拟未回填的历史数据。
		},
	}
	client := &fakeUsageClient{}
	svc := NewUsageService(store, client, nil)

	view, err := svc.GetOrgUsage(context.Background(), platformAdmin(), orgID, 0, 0)
	require.NoError(t, err)
	assert.Empty(t, view.Items)
	assert.Equal(t, int64(0), client.lastUserQuotaUserID, "username 空时不应调 newapi")
}

// TestGetAppUsageUsesAppNewapiKeyName 校验 GetAppUsage 在 store 能取到 app 且 app.NewapiKeyName 非空时，
// TokenName 使用数据库字段而非约定派生。
func TestGetAppUsageUsesAppNewapiKeyName(t *testing.T) {
	appID := mustUUID(t, "0193ce63-4b8e-7000-a000-000000000001")
	store := &fakeUsageStore{
		appByID: map[string]sqlc.App{
			appID: {
				ID:            appID,
				NewapiKeyID:   null.StringFrom("42"),
				NewapiKeyName: null.StringFrom("app-database-override"),
			},
		},
	}
	client := &fakeUsageClient{tokenLogs: newapi.LogsPage{Items: []newapi.LogEntry{{ID: 1}}, Total: 1}}
	svc := NewUsageService(store, client, nil)

	_, err := svc.GetAppUsage(context.Background(), platformAdmin(),
		appID,
		42,
		LogsQueryOptions{Page: 1, PageSize: 50})
	require.NoError(t, err)
	assert.Equal(t, "app-database-override", client.lastTokenLogsQuery.TokenName,
		"GetAppUsage 应优先用数据库里写的 NewapiKeyName 而非约定派生")
}

// TestUsageServiceAppRejectsNonOwnerMember 是 IDOR 越权的回归用例：组织成员请求另一名成员
// 拥有的应用用量。修复前调用方可把 owner_user_id 伪造成自己借 CanViewApp(member) 分支放行；
// 修复后鉴权基于数据库中应用的真实归属（OwnerUserID=victim），组织成员必须被拒绝。
func TestUsageServiceAppRejectsNonOwnerMember(t *testing.T) {
	attackerID := "00000000-0000-0000-0000-0000000000a1" // 发起越权的组织成员
	victimID := "00000000-0000-0000-0000-0000000000a2"   // 应用真正的拥有者
	appID := mustUUID(t, "0193ce63-4b8e-7000-a000-0000000000ff")
	store := &fakeUsageStore{
		appByID: map[string]sqlc.App{
			appID: {ID: appID, OrgID: "org-1", OwnerUserID: victimID, NewapiKeyName: null.StringFrom("app-victim")},
		},
	}
	client := &fakeUsageClient{tokenLogs: newapi.LogsPage{Items: []newapi.LogEntry{{ID: 1}}, Total: 1}}
	svc := NewUsageService(store, client, nil)

	_, err := svc.GetAppUsage(context.Background(), auth.Principal{
		Role:   domain.UserRoleOrgMember,
		OrgID:  "org-1",
		UserID: attackerID,
	}, appID, 42, LogsQueryOptions{})
	require.ErrorIs(t, err, ErrForbidden)
	// 越权被拦下时不得真正调用 new-api 拉日志。
	assert.Equal(t, 0, client.tokenLogsCalls)
}

// TestUsageServiceAppAllowsOwnerMember 校验正向路径：组织成员读取本人拥有的应用用量，
// 鉴权按真实归属（OwnerUserID==自己）通过并返回日志。
func TestUsageServiceAppAllowsOwnerMember(t *testing.T) {
	memberID := "00000000-0000-0000-0000-0000000000a1"
	appID := mustUUID(t, "0193ce63-4b8e-7000-a000-0000000000fe")
	store := &fakeUsageStore{
		appByID: map[string]sqlc.App{
			appID: {ID: appID, OrgID: "org-1", OwnerUserID: memberID, NewapiKeyName: null.StringFrom("app-self")},
		},
	}
	client := &fakeUsageClient{tokenLogs: newapi.LogsPage{Items: []newapi.LogEntry{{ID: 9}}, Total: 1}}
	svc := NewUsageService(store, client, nil)

	view, err := svc.GetAppUsage(context.Background(), auth.Principal{
		Role:   domain.UserRoleOrgMember,
		OrgID:  "org-1",
		UserID: memberID,
	}, appID, 42, LogsQueryOptions{})
	require.NoError(t, err)
	require.Len(t, view.Items, 1)
	assert.Equal(t, "app-self", client.lastTokenLogsQuery.TokenName)
}

// TestGetOrgUsageBreakdownForbidsNonPlatformAdmin 校验非 platform_admin 拿不到分组用量。
func TestGetOrgUsageBreakdownForbidsNonPlatformAdmin(t *testing.T) {
	svc := NewUsageService(&fakeUsageStore{}, &fakeUsageClient{}, nil)
	_, err := svc.GetOrgUsageBreakdown(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin}, 0, 0)
	require.ErrorIs(t, err, ErrForbidden)
}

// TestGetOrgUsageBreakdownSkipsOrgsWithoutNewAPIUser 校验无 newapi_user_id 或 newapi_username 的组织被静默跳过。
func TestGetOrgUsageBreakdownSkipsOrgsWithoutNewAPIUser(t *testing.T) {
	orgWithUser := sqlc.Organization{
		ID:             mustUUID(t, "00000000-0000-0000-0000-000000000a01"),
		Name:           "org-with-user",
		NewapiUserID:   null.StringFrom("10"),
		NewapiUsername: null.StringFrom("org-10-user"),
	}
	orgWithout := sqlc.Organization{
		ID:   mustUUID(t, "00000000-0000-0000-0000-000000000a02"),
		Name: "org-without-user",
		// NewapiUserID / NewapiUsername 均为零值（null.String{}），模拟未初始化的组织
	}
	store := &fakeUsageStore{allActiveOrgs: []sqlc.Organization{orgWithUser, orgWithout}}
	client := &fakeUsageClient{userQuota: []newapi.QuotaDate{{Date: "2026-05-22", Quota: 500}}}
	svc := NewUsageService(store, client, nil)

	result, err := svc.GetOrgUsageBreakdown(context.Background(), platformAdmin(), 0, 0)
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "org-with-user", result.Items[0].OrgName)
	assert.Equal(t, int64(500), result.Items[0].TotalQuota)
}

// TestGetOrgUsageBreakdownSortsAndCapsAt10 校验结果按 TotalQuota 降序并截取前 10 条。
func TestGetOrgUsageBreakdownSortsAndCapsAt10(t *testing.T) {
	orgs := make([]sqlc.Organization, 12)
	for i := range orgs {
		idStr := fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)
		orgs[i] = sqlc.Organization{
			ID:             mustUUID(t, idStr),
			Name:           fmt.Sprintf("org-%02d", i+1),
			NewapiUserID:   null.StringFrom(fmt.Sprintf("%d", i+1)),
			NewapiUsername: null.StringFrom(fmt.Sprintf("user-%d", i+1)),
		}
	}
	client := &fakeUsageClientWithPerUserQuota{
		quotaByUserID: func(id int64) int64 { return id * 100 },
	}
	store := &fakeUsageStore{allActiveOrgs: orgs}
	svc := NewUsageService(store, client, nil)

	result, err := svc.GetOrgUsageBreakdown(context.Background(), platformAdmin(), 0, 0)
	require.NoError(t, err)
	require.Len(t, result.Items, 10)
	assert.Equal(t, int64(1200), result.Items[0].TotalQuota)
	for i := 1; i < len(result.Items); i++ {
		assert.GreaterOrEqual(t, result.Items[i-1].TotalQuota, result.Items[i].TotalQuota,
			"结果应按 TotalQuota 降序排列")
	}
}

// fakeUsageClientWithPerUserQuota 是支持按 userID 返回不同 quota 的实现，专用于排序测试。
type fakeUsageClientWithPerUserQuota struct {
	quotaByUserID func(id int64) int64
}

func (c *fakeUsageClientWithPerUserQuota) GetTokenLogs(_ context.Context, _ newapi.LogsQuery) (newapi.LogsPage, error) {
	return newapi.LogsPage{}, nil
}

func (c *fakeUsageClientWithPerUserQuota) GetUserQuotaDates(_ context.Context, userID int64, _ string, _, _ int64) ([]newapi.QuotaDate, error) {
	return []newapi.QuotaDate{{Date: "2026-05-22", Quota: c.quotaByUserID(userID)}}, nil
}

func (c *fakeUsageClientWithPerUserQuota) GetAllQuotaDates(_ context.Context, _, _ int64) ([]newapi.QuotaDate, error) {
	return nil, nil
}
