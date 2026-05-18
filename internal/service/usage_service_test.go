package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/store/sqlc"
)

// TestUsageServiceForbidsCrossOrg 校验非平台管理员只能看自己 org 的用量。
func TestUsageServiceForbidsCrossOrg(t *testing.T) {
	svc := NewUsageService(&fakeUsageStore{}, &fakeUsageClient{}, nil)
	_, err := svc.GetAppUsage(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "other"}, "app-1", "owner-org", "owner-user", 1, LogsQueryOptions{})
	require.ErrorIs(t, err, ErrForbidden)
}

// TestUsageServiceAppProxiesTokenLogs 校验 GetAppUsage 直接调 GetTokenLogs(token_id=…)
// 并把返回 items 透传到 LogsPage.Items。
func TestUsageServiceAppProxiesTokenLogs(t *testing.T) {
	client := &fakeUsageClient{tokenLogs: newapi.LogsPage{
		Items: []newapi.LogEntry{{ID: 1, ModelName: "qwen2.5", Quota: 100}},
		Total: 1,
	}}
	svc := NewUsageService(&fakeUsageStore{}, client, nil)

	view, err := svc.GetAppUsage(context.Background(), platformAdmin(), "app-1", "owner-org", "owner-user", 42, LogsQueryOptions{Page: 1, PageSize: 50})
	require.NoError(t, err)
	// fakeUsageStore.GetApp 默认返回 ErrNoRows，service 走回退路径拼 "app-"+appID。
	if client.lastTokenLogsQuery.TokenName != "app-app-1" || client.lastTokenLogsQuery.PageSize != 50 {
		t.Fatalf("token logs query = %+v", client.lastTokenLogsQuery)
	}
	if len(view.Items) != 1 || view.Items[0].Quota != 100 {
		t.Fatalf("view = %+v", view)
	}
}

// TestUsageServiceAppMapsErrors 校验 newapi.ErrNotFound / ErrUnauthorized 的映射。
func TestUsageServiceAppMapsErrors(t *testing.T) {
	svc := NewUsageService(&fakeUsageStore{}, &fakeUsageClient{tokenLogsError: newapi.ErrNotFound}, nil)
	if _, err := svc.GetAppUsage(context.Background(), platformAdmin(), "app", "o", "u", 1, LogsQueryOptions{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}

	svc = NewUsageService(&fakeUsageStore{}, &fakeUsageClient{tokenLogsError: newapi.ErrUnauthorized}, nil)
	if _, err := svc.GetAppUsage(context.Background(), platformAdmin(), "app", "o", "u", 1, LogsQueryOptions{}); !errors.Is(err, ErrUsageUnavailable) {
		t.Fatalf("error = %v, want ErrUsageUnavailable", err)
	}
}

// TestUsageServiceAppZeroKeyReturnsEmpty 校验 newapiKeyID=0（应用尚未初始化）时直接返回空 LogsPage。
func TestUsageServiceAppZeroKeyReturnsEmpty(t *testing.T) {
	client := &fakeUsageClient{}
	svc := NewUsageService(&fakeUsageStore{}, client, nil)
	view, err := svc.GetAppUsage(context.Background(), platformAdmin(), "app", "o", "u", 0, LogsQueryOptions{})
	require.NoError(t, err)
	require.Equal(t, 0, len(view.Items))
	require.Equal(t, 0, client.tokenLogsCalls)
}

// TestUsageServiceMissingClient 校验 client=nil 时 ErrUsageUnavailable。
func TestUsageServiceMissingClient(t *testing.T) {
	svc := NewUsageService(&fakeUsageStore{}, nil, nil)
	if _, err := svc.GetAppUsage(context.Background(), platformAdmin(), "app", "o", "u", 1, LogsQueryOptions{}); !errors.Is(err, ErrUsageUnavailable) {
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
			NewapiKeyID:   pgtype.Text{String: "77", Valid: true},
			NewapiKeyName: pgtype.Text{String: "app-fixed-uuid-for-test", Valid: true},
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
			NewapiKeyID:   pgtype.Text{String: "77", Valid: true},
			NewapiKeyName: pgtype.Text{String: "app-self-token", Valid: true},
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
	// org_member 走 self 路径，service 同样应按 app.NewapiKeyName 透传 token_name。
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

// TestUsageServiceOrgUsesNewapiUserID 校验 org 维度查 organizations.newapi_user_id 后调
// GetUserQuotaDates；新契约下必须同时透传 newapi_username 给 client 做客户端过滤。
func TestUsageServiceOrgUsesNewapiUserID(t *testing.T) {
	orgUUID := mustUUID(t, "00000000-0000-0000-0000-000000000b01")
	store := &fakeUsageStore{
		org: sqlc.Organization{
			ID:             orgUUID,
			NewapiUserID:   pgtype.Text{String: "55", Valid: true},
			NewapiUsername: pgtype.Text{String: "org-55-user", Valid: true},
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
	svc := NewUsageService(&fakeUsageStore{}, client, auditor)
	_, err := svc.GetAppUsage(context.Background(), platformAdmin(), "app-1", "owner-org", "owner-user", 42, LogsQueryOptions{})
	require.Error(t, err)
	require.Equal(t, 1, len(auditor.events))
	ev := auditor.events[0]
	require.Equal(t, "owner-org", ev.OrgID)
	require.Equal(t, "GET /api/log/?token_id=...", ev.Endpoint)
}

// TestUsageService_MemberUsageFailureRecordsAudit 校验 GetMemberUsage new-api 调用失败时触发审计。
func TestUsageService_MemberUsageFailureRecordsAudit(t *testing.T) {
	auditor := &fakeFailAuditor{}
	store := &fakeUsageStore{
		activeApp: sqlc.App{
			NewapiKeyID: pgtype.Text{String: "77", Valid: true},
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
	require.Equal(t, "GET /api/log/?token_id=...", ev.Endpoint)
}

// TestUsageService_OrgUsageFailureRecordsAudit 校验 GetOrgUsage new-api 调用失败时触发审计。
// 新契约要求 username 必须有值才会真正调上游，因此 fixture 同时填 user_id 与 username。
func TestUsageService_OrgUsageFailureRecordsAudit(t *testing.T) {
	auditor := &fakeFailAuditor{}
	orgID := "00000000-0000-0000-0000-000000000b01"
	orgUUID := mustUUID(t, orgID)
	store := &fakeUsageStore{
		org: sqlc.Organization{
			ID:             orgUUID,
			NewapiUserID:   pgtype.Text{String: "55", Valid: true},
			NewapiUsername: pgtype.Text{String: "org-55-user", Valid: true},
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
type fakeUsageStore struct {
	activeApp       sqlc.App
	activeAppCalled bool
	lastActiveOwner pgtype.UUID
	org             sqlc.Organization
}

func (s *fakeUsageStore) GetApp(_ context.Context, _ pgtype.UUID) (sqlc.App, error) {
	return sqlc.App{}, pgx.ErrNoRows
}

func (s *fakeUsageStore) GetActiveAppByOwner(_ context.Context, ownerID pgtype.UUID) (sqlc.App, error) {
	s.activeAppCalled = true
	s.lastActiveOwner = ownerID
	return s.activeApp, nil
}

func (s *fakeUsageStore) GetOrganization(_ context.Context, id pgtype.UUID) (sqlc.Organization, error) {
	if s.org.ID.Valid && s.org.ID == id {
		return s.org, nil
	}
	return sqlc.Organization{}, pgx.ErrNoRows
}

// TestGetAppUsageFallsBackWhenKeyNameEmpty 校验 app 维度 store.GetApp 返回 ErrNoRows
// 时，service 回退到 "app-"+appID 的拼装路径，确保历史/未回填数据仍然可查。
// fakeUsageStore.GetApp 默认就返回 ErrNoRows，因此这里直接断言回退结果。
func TestGetAppUsageFallsBackWhenKeyNameEmpty(t *testing.T) {
	// 当 GetApp 拿不到记录时，service 不应中断查询，而是按约定拼 "app-"+UUID。
	client := &fakeUsageClient{tokenLogs: newapi.LogsPage{Items: []newapi.LogEntry{{ID: 1}}, Total: 1}}
	svc := NewUsageService(&fakeUsageStore{}, client, nil)

	_, err := svc.GetAppUsage(context.Background(), platformAdmin(),
		"0193ce63-4b8e-7000-a000-000000000001",
		"owner-org", "owner-user",
		42,
		LogsQueryOptions{Page: 1, PageSize: 50})
	require.NoError(t, err)
	// 回退路径直接以传入的 appID 拼出 token_name。
	assert.Equal(t, "app-0193ce63-4b8e-7000-a000-000000000001", client.lastTokenLogsQuery.TokenName)
	assert.Equal(t, 50, client.lastTokenLogsQuery.PageSize)
}

// TestGetMemberUsageUsesAppNewapiKeyName 校验 member 维度走 GetActiveAppByOwner
// 拿到 app 后用 app.NewapiKeyName 作 TokenName，确保 token 维度过滤口径
// 与数据库中实际写入的 key_name 完全一致。
func TestGetMemberUsageUsesAppNewapiKeyName(t *testing.T) {
	appID := mustUUID(t, "0193ce63-4b8e-7000-a000-000000000002")
	store := &fakeUsageStore{
		// NewapiKeyName 模拟初始化时由 app_initialize 写入的 token 名。
		activeApp: sqlc.App{
			ID:            appID,
			NewapiKeyID:   pgtype.Text{String: "77", Valid: true},
			NewapiKeyName: pgtype.Text{String: "app-0193ce63-4b8e-7000-a000-000000000002", Valid: true},
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

// TestGetMemberUsageFallsBackWhenKeyNameEmpty 校验 app.NewapiKeyName 字段空时
// （历史/未回填的边界）service 自行拼 "app-"+app.ID，避免该路径直接抛错。
func TestGetMemberUsageFallsBackWhenKeyNameEmpty(t *testing.T) {
	appID := mustUUID(t, "0193ce63-4b8e-7000-a000-000000000003")
	store := &fakeUsageStore{
		// 故意不设 NewapiKeyName 以模拟旧数据未回填的边界。
		activeApp: sqlc.App{
			ID:          appID,
			NewapiKeyID: pgtype.Text{String: "78", Valid: true},
		},
	}
	client := &fakeUsageClient{tokenLogs: newapi.LogsPage{}}
	svc := NewUsageService(store, client, nil)

	_, err := svc.GetMemberUsage(context.Background(), platformAdmin(),
		"00000000-0000-0000-0000-000000000b01",
		"00000000-0000-0000-0000-000000000c01",
		LogsQueryOptions{})
	require.NoError(t, err)
	// 字段空回退到 "app-"+app.ID。
	assert.Equal(t, "app-0193ce63-4b8e-7000-a000-000000000003", client.lastTokenLogsQuery.TokenName)
}

// TestGetOrgUsagePassesUsernameToClient 校验 GetOrgUsage 把
// organizations.newapi_username 透传给 client 做客户端过滤，
// 避免 new-api admin /api/data/users?id= 静默忽略导致跨组织数据混淆。
func TestGetOrgUsagePassesUsernameToClient(t *testing.T) {
	orgID := mustUUID(t, "0193ce63-4b8e-7000-a000-0000000000aa")
	store := &fakeUsageStore{
		org: sqlc.Organization{
			ID:             orgID,
			NewapiUserID:   pgtype.Text{String: "14", Valid: true},
			NewapiUsername: pgtype.Text{String: "m8-test", Valid: true},
		},
	}
	client := &fakeUsageClient{userQuota: []newapi.QuotaDate{{Date: "2026-05-19", Quota: 7}}}
	svc := NewUsageService(store, client, nil)

	_, err := svc.GetOrgUsage(context.Background(), platformAdmin(), uuidToString(orgID), 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(14), client.lastUserQuotaUserID)
	// service 必须把 organizations.newapi_username 原样透给 client。
	assert.Equal(t, "m8-test", client.lastUserQuotaUsername)
}

// TestGetOrgUsageReturnsEmptyWhenUsernameEmpty 校验老数据 / 未回填边界：
// organizations.newapi_username 为空时，service 直接返回空 series，
// 不调 newapi（避免拿一堆其他组织的数据来污染）。
func TestGetOrgUsageReturnsEmptyWhenUsernameEmpty(t *testing.T) {
	orgID := mustUUID(t, "0193ce63-4b8e-7000-a000-0000000000bb")
	store := &fakeUsageStore{
		// NewapiUsername 故意留空，模拟未回填的历史数据。
		org: sqlc.Organization{
			ID:           orgID,
			NewapiUserID: pgtype.Text{String: "99", Valid: true},
		},
	}
	client := &fakeUsageClient{}
	svc := NewUsageService(store, client, nil)

	view, err := svc.GetOrgUsage(context.Background(), platformAdmin(), uuidToString(orgID), 0, 0)
	require.NoError(t, err)
	assert.Empty(t, view.Items)
	// username 缺失时绝不能调上游，否则会把别人组织的数据带回来。
	assert.Equal(t, int64(0), client.lastUserQuotaUserID, "username 空时不应调 newapi")
}
