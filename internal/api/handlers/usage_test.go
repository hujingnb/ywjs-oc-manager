package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// usageServiceStub 实现 usageService 接口，仅 stub 测试用到的方法。
type usageServiceStub struct {
	memberResult   service.LogsPage
	memberErr      error
	orgResult      service.QuotaSeries
	orgErr         error
	platformResult service.QuotaSeries
	platformErr    error
	appResult      service.LogsPage
	appErr         error

	lastMemberOrgID   string
	lastMemberUserID  string
	lastOrgSince      int64
	lastOrgUntil      int64
	lastPlatformSince int64
	lastPlatformUntil int64
}

func (s *usageServiceStub) GetMemberUsage(_ context.Context, _ auth.Principal, orgID, userID string, _ service.LogsQueryOptions) (service.LogsPage, error) {
	s.lastMemberOrgID = orgID
	s.lastMemberUserID = userID
	return s.memberResult, s.memberErr
}

func (s *usageServiceStub) GetOrgUsage(_ context.Context, _ auth.Principal, _ string, since, until int64) (service.QuotaSeries, error) {
	s.lastOrgSince = since
	s.lastOrgUntil = until
	return s.orgResult, s.orgErr
}

func (s *usageServiceStub) GetPlatformUsage(_ context.Context, _ auth.Principal, since, until int64) (service.QuotaSeries, error) {
	s.lastPlatformSince = since
	s.lastPlatformUntil = until
	return s.platformResult, s.platformErr
}

func (s *usageServiceStub) GetAppUsage(_ context.Context, _ auth.Principal, _, _, _ string, _ int64, _ service.LogsQueryOptions) (service.LogsPage, error) {
	return s.appResult, s.appErr
}

// newUsageTestRouter 构建用于测试的 gin router。
func newUsageTestRouter(t *testing.T, svc usageService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterUsageRoutes(router, NewUsageHandler(svc))
	return router
}

// TestUsageGetMemberHappy 验证用量获取成员成功路径的成功路径场景。
func TestUsageGetMemberHappy(t *testing.T) {
	stub := &usageServiceStub{memberResult: service.LogsPage{Total: 5}}
	router := newUsageTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/members/u1?org_id=org-1", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "usage")
	assert.Equal(t, "org-1", stub.lastMemberOrgID)
	assert.Equal(t, "u1", stub.lastMemberUserID)
}

// TestUsageGetMemberMissingOrgID 验证用量获取成员缺失组织ID的异常或拒绝路径场景。
func TestUsageGetMemberMissingOrgID(t *testing.T) {
	stub := &usageServiceStub{}
	router := newUsageTestRouter(t, stub)

	w := httptest.NewRecorder()
	// 缺少必填的 org_id 参数
	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/members/u1", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestUsageGetMemberForbidden 验证用量获取成员禁止访问的异常或拒绝路径场景。
func TestUsageGetMemberForbidden(t *testing.T) {
	stub := &usageServiceStub{memberErr: service.ErrForbidden}
	router := newUsageTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/members/u2?org_id=org-1", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestUsageGetOrgHappy 验证用量获取组织成功路径的成功路径场景。
func TestUsageGetOrgHappy(t *testing.T) {
	stub := &usageServiceStub{orgResult: service.QuotaSeries{}}
	router := newUsageTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/organizations/org-1", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "usage")
}

// TestUsageGetOrgAppliesDefaultWindow 验证组织用量缺省查询时间窗口时应用默认 30 天范围的场景。
func TestUsageGetOrgAppliesDefaultWindow(t *testing.T) {
	stub := &usageServiceStub{orgResult: service.QuotaSeries{}}
	router := newUsageTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/organizations/org-1", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Greater(t, stub.lastOrgSince, int64(0))
	assert.Greater(t, stub.lastOrgUntil, stub.lastOrgSince)
	assert.InDelta(t, int64(30*24*60*60), stub.lastOrgUntil-stub.lastOrgSince, 5)
}

// TestUsageGetOrgKeepsExplicitWindow 验证组织用量传入显式 since/until 时保留调用方时间窗口的场景。
func TestUsageGetOrgKeepsExplicitWindow(t *testing.T) {
	stub := &usageServiceStub{orgResult: service.QuotaSeries{}}
	router := newUsageTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/organizations/org-1?since=100&until=200", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(100), stub.lastOrgSince)
	assert.Equal(t, int64(200), stub.lastOrgUntil)
}

// TestUsageGetPlatformForbidden 验证用量获取平台禁止访问的异常或拒绝路径场景。
func TestUsageGetPlatformForbidden(t *testing.T) {
	stub := &usageServiceStub{platformErr: service.ErrForbidden}
	router := newUsageTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/platform", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestUsageGetPlatformHappy 验证用量获取平台成功路径的成功路径场景。
func TestUsageGetPlatformHappy(t *testing.T) {
	stub := &usageServiceStub{platformResult: service.QuotaSeries{}}
	router := newUsageTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/platform", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

// TestUsageGetPlatformAppliesDefaultWindow 验证平台用量缺省查询时间窗口时应用默认 30 天范围的场景。
func TestUsageGetPlatformAppliesDefaultWindow(t *testing.T) {
	stub := &usageServiceStub{platformResult: service.QuotaSeries{}}
	router := newUsageTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/platform", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Greater(t, stub.lastPlatformSince, int64(0))
	assert.Greater(t, stub.lastPlatformUntil, stub.lastPlatformSince)
	assert.InDelta(t, int64(30*24*60*60), stub.lastPlatformUntil-stub.lastPlatformSince, 5)
}

// TestUsageGetAppHappy 验证用量获取应用成功路径的成功路径场景。
func TestUsageGetAppHappy(t *testing.T) {
	stub := &usageServiceStub{appResult: service.LogsPage{Total: 3}}
	router := newUsageTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/usage?owner_org_id=org-1&owner_user_id=u1", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "usage")
}

// TestUsageGetAppMissingParams 验证用量获取应用缺失参数的异常或拒绝路径场景。
func TestUsageGetAppMissingParams(t *testing.T) {
	stub := &usageServiceStub{}
	router := newUsageTestRouter(t, stub)

	w := httptest.NewRecorder()
	// 缺少 owner_user_id
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/usage?owner_org_id=org-1", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

