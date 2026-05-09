package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/store/sqlc"
	"github.com/stretchr/testify/require"
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
	if client.lastTokenLogsQuery.TokenID != 42 || client.lastTokenLogsQuery.PageSize != 50 {
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
// 拿到 app.newapi_key_id 再调 GetTokenLogs。
func TestUsageServiceMemberUsesActiveAppByOwner(t *testing.T) {
	memberID := mustUUID(t, "00000000-0000-0000-0000-000000000c01")
	store := &fakeUsageStore{
		activeApp: sqlc.App{
			NewapiKeyID: pgtype.Text{String: "77", Valid: true},
		},
	}
	client := &fakeUsageClient{tokenLogs: newapi.LogsPage{Items: []newapi.LogEntry{{ID: 9}}, Total: 1}}
	svc := NewUsageService(store, client, nil)
	view, err := svc.GetMemberUsage(context.Background(), platformAdmin(), "00000000-0000-0000-0000-000000000b01", "00000000-0000-0000-0000-000000000c01", LogsQueryOptions{})
	require.NoError(t, err)
	if !store.activeAppCalled || store.lastActiveOwner != memberID {
		t.Fatalf("expected GetActiveAppByOwner, got=%v owner=%v", store.activeAppCalled, store.lastActiveOwner)
	}
	if client.lastTokenLogsQuery.TokenID != 77 || len(view.Items) != 1 {
		t.Fatalf("view = %+v query = %+v", view, client.lastTokenLogsQuery)
	}
}

// TestUsageServiceOrgUsesNewapiUserID 校验 org 维度查 organizations.newapi_user_id 后调
// GetUserQuotaDates。
func TestUsageServiceOrgUsesNewapiUserID(t *testing.T) {
	orgUUID := mustUUID(t, "00000000-0000-0000-0000-000000000b01")
	store := &fakeUsageStore{
		org: sqlc.Organization{
			ID:           orgUUID,
			NewapiUserID: pgtype.Text{String: "55", Valid: true},
		},
	}
	client := &fakeUsageClient{userQuota: []newapi.QuotaDate{{Date: "2026-05-01", Quota: 100}}}
	svc := NewUsageService(store, client, nil)
	view, err := svc.GetOrgUsage(context.Background(), platformAdmin(), "00000000-0000-0000-0000-000000000b01", 0, 0)
	require.NoError(t, err)
	require.Equal(t, int64(55), client.lastUserQuotaUserID)
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
func TestUsageService_OrgUsageFailureRecordsAudit(t *testing.T) {
	auditor := &fakeFailAuditor{}
	orgID := "00000000-0000-0000-0000-000000000b01"
	orgUUID := mustUUID(t, orgID)
	store := &fakeUsageStore{
		org: sqlc.Organization{
			ID:           orgUUID,
			NewapiUserID: pgtype.Text{String: "55", Valid: true},
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
	tokenLogs           newapi.LogsPage
	tokenLogsError      error
	tokenLogsCalls      int
	lastTokenLogsQuery  newapi.LogsQuery
	userQuota           []newapi.QuotaDate
	userQuotaError      error
	lastUserQuotaUserID int64
	allQuota            []newapi.QuotaDate
	allQuotaError       error
	allQuotaCalled      bool
}

func (c *fakeUsageClient) GetTokenLogs(_ context.Context, q newapi.LogsQuery) (newapi.LogsPage, error) {
	c.tokenLogsCalls++
	c.lastTokenLogsQuery = q
	if c.tokenLogsError != nil {
		return newapi.LogsPage{}, c.tokenLogsError
	}
	return c.tokenLogs, nil
}

func (c *fakeUsageClient) GetUserQuotaDates(_ context.Context, userID, _, _ int64) ([]newapi.QuotaDate, error) {
	c.lastUserQuotaUserID = userID
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
