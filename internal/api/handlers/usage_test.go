package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
}

func (s *usageServiceStub) GetMemberUsage(_ context.Context, _ auth.Principal, _, _ string, _ service.LogsQueryOptions) (service.LogsPage, error) {
	return s.memberResult, s.memberErr
}

func (s *usageServiceStub) GetOrgUsage(_ context.Context, _ auth.Principal, _ string, _, _ int64) (service.QuotaSeries, error) {
	return s.orgResult, s.orgErr
}

func (s *usageServiceStub) GetPlatformUsage(_ context.Context, _ auth.Principal, _, _ int64) (service.QuotaSeries, error) {
	return s.platformResult, s.platformErr
}

func (s *usageServiceStub) GetAppUsage(_ context.Context, _ auth.Principal, _, _, _ string, _ int64, _ service.LogsQueryOptions) (service.LogsPage, error) {
	return s.appResult, s.appErr
}

// newUsageTestRouter 构建用于测试的 gin router + token manager。
func newUsageTestRouter(t *testing.T, svc usageService) (*gin.Engine, *auth.TokenManager) {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	tokens, err := auth.NewTokenManager("a", "b", time.Minute, time.Hour)
	require.NoError(t, err)
	router := gin.New()
	RegisterUsageRoutes(router, NewUsageHandler(svc, tokens))
	return router, tokens
}

func TestUsageGetMemberHappy(t *testing.T) {
	stub := &usageServiceStub{memberResult: service.LogsPage{Total: 5}}
	router, tokens := newUsageTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/members/u1?org_id=org-1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "usage")
}

func TestUsageGetMemberMissingOrgID(t *testing.T) {
	stub := &usageServiceStub{}
	router, tokens := newUsageTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})

	w := httptest.NewRecorder()
	// 缺少必填的 org_id 参数
	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/members/u1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUsageGetMemberForbidden(t *testing.T) {
	stub := &usageServiceStub{memberErr: service.ErrForbidden}
	router, tokens := newUsageTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/members/u2?org_id=org-1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestUsageGetOrgHappy(t *testing.T) {
	stub := &usageServiceStub{orgResult: service.QuotaSeries{}}
	router, tokens := newUsageTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/organizations/org-1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "usage")
}

func TestUsageGetPlatformForbidden(t *testing.T) {
	stub := &usageServiceStub{platformErr: service.ErrForbidden}
	router, tokens := newUsageTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/platform", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestUsageGetPlatformHappy(t *testing.T) {
	stub := &usageServiceStub{platformResult: service.QuotaSeries{}}
	router, tokens := newUsageTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/platform", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

func TestUsageGetAppHappy(t *testing.T) {
	stub := &usageServiceStub{appResult: service.LogsPage{Total: 3}}
	router, tokens := newUsageTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/usage?owner_org_id=org-1&owner_user_id=u1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "usage")
}

func TestUsageGetAppMissingParams(t *testing.T) {
	stub := &usageServiceStub{}
	router, tokens := newUsageTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	w := httptest.NewRecorder()
	// 缺少 owner_user_id
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/usage?owner_org_id=org-1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUsageRequiresToken(t *testing.T) {
	stub := &usageServiceStub{}
	router, _ := newUsageTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/platform", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
