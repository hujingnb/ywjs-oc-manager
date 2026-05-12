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

// appsStub 实现 appService 接口，仅 stub 测试用到的方法。
type appsStub struct {
	getResult  service.AppResult
	getErr     error
	listResult []service.AppResult
	listErr    error
}

func (s *appsStub) Get(_ context.Context, _ auth.Principal, _ string) (service.AppResult, error) {
	return s.getResult, s.getErr
}

func (s *appsStub) ListByOrg(_ context.Context, _ auth.Principal, _ string, _, _ int32) ([]service.AppResult, error) {
	return s.listResult, s.listErr
}

// newAppsTestRouter 构建用于测试的 gin router + token manager。
func newAppsTestRouter(t *testing.T, svc appService) (*gin.Engine, *auth.TokenManager) {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	tokens, err := auth.NewTokenManager("a", "b", time.Minute, time.Hour)
	require.NoError(t, err)
	router := gin.New()
	RegisterAppRoutes(router, NewAppsHandler(svc, tokens))
	return router, tokens
}

// TestAppsListHappy 验证应用列表成功路径的成功路径场景。
func TestAppsListHappy(t *testing.T) {
	stub := &appsStub{listResult: []service.AppResult{{ID: "app-1", Name: "测试应用"}}}
	router, tokens := newAppsTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/apps", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "app-1")
}

// TestAppsListForbidden 验证应用列表禁止访问的异常或拒绝路径场景。
func TestAppsListForbidden(t *testing.T) {
	stub := &appsStub{listErr: service.ErrForbidden}
	router, tokens := newAppsTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-2/apps", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestAppsListRequiresToken 验证应用列表要求令牌的预期行为场景。
func TestAppsListRequiresToken(t *testing.T) {
	stub := &appsStub{}
	router, _ := newAppsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/apps", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestAppsGetHappy 验证应用获取成功路径的成功路径场景。
func TestAppsGetHappy(t *testing.T) {
	stub := &appsStub{getResult: service.AppResult{ID: "app-1", Name: "测试应用"}}
	router, tokens := newAppsTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "app-1")
}

// TestAppsGetNotFound 验证应用获取未找到的异常或拒绝路径场景。
func TestAppsGetNotFound(t *testing.T) {
	stub := &appsStub{getErr: service.ErrNotFound}
	router, tokens := newAppsTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/missing", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
