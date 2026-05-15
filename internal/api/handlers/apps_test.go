package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// appsStub 实现 appService 接口，仅 stub 测试用到的方法。
type appsStub struct {
	getResult   service.AppResult
	getErr      error
	listResult  []service.AppResult
	listErr     error
	modelResult service.AppModelUpdateResult
	modelErr    error
	lastAppID   string
	lastModelID string
}

func (s *appsStub) Get(_ context.Context, _ auth.Principal, _ string) (service.AppResult, error) {
	return s.getResult, s.getErr
}

func (s *appsStub) ListByOrg(_ context.Context, _ auth.Principal, _ string, _, _ int32) ([]service.AppResult, error) {
	return s.listResult, s.listErr
}

func (s *appsStub) UpdateModel(_ context.Context, _ auth.Principal, appID, modelID string) (service.AppModelUpdateResult, error) {
	s.lastAppID = appID
	s.lastModelID = modelID
	return s.modelResult, s.modelErr
}

// newAppsTestRouter 构建用于测试的 gin router。
func newAppsTestRouter(t *testing.T, svc appService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterAppRoutes(router, NewAppsHandler(svc))
	return router
}

// TestAppsListHappy 验证应用列表成功路径的成功路径场景。
func TestAppsListHappy(t *testing.T) {
	stub := &appsStub{listResult: []service.AppResult{{ID: "app-1", Name: "测试应用"}}}
	router := newAppsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/apps", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "app-1")
}

// TestAppsListForbidden 验证应用列表禁止访问的异常或拒绝路径场景。
func TestAppsListForbidden(t *testing.T) {
	stub := &appsStub{listErr: service.ErrForbidden}
	router := newAppsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-2/apps", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestAppsGetHappy 验证应用获取成功路径的成功路径场景。
func TestAppsGetHappy(t *testing.T) {
	stub := &appsStub{getResult: service.AppResult{ID: "app-1", Name: "测试应用"}}
	router := newAppsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "app-1")
}

// TestAppsGetNotFound 验证应用获取未找到的异常或拒绝路径场景。
func TestAppsGetNotFound(t *testing.T) {
	stub := &appsStub{getErr: service.ErrNotFound}
	router := newAppsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/missing", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestAppsUpdateModelForwardsRequest 验证模型修改路由转发 appId 和 model_id。
func TestAppsUpdateModelForwardsRequest(t *testing.T) {
	stub := &appsStub{modelResult: service.AppModelUpdateResult{App: service.AppResult{ID: "app-1", ModelID: "qwen2.5:7b"}}}
	router := newAppsTestRouter(t, stub)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/apps/app-1/model", strings.NewReader(`{"model_id":"qwen2.5:7b"}`))
	req = withPrincipal(req, auth.Principal{UserID: "u-1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, "app-1", stub.lastAppID)
	assert.Equal(t, "qwen2.5:7b", stub.lastModelID)
}

// TestAppsUpdateModelMapsInvalidModel 验证非法模型返回 400。
func TestAppsUpdateModelMapsInvalidModel(t *testing.T) {
	stub := &appsStub{modelErr: service.ErrMemberCreateInvalid}
	router := newAppsTestRouter(t, stub)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/apps/app-1/model", strings.NewReader(`{"model_id":"missing"}`))
	req = withPrincipal(req, auth.Principal{UserID: "u-1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusBadRequest, resp.Code)
}
