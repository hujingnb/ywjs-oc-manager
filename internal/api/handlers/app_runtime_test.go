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

// runtimeOpServiceStub 实现 runtimeOperationService 接口，仅 stub 测试用到的方法。
type runtimeOpServiceStub struct {
	triggerResult service.RuntimeOperationResult
	triggerErr    error
	initResult    service.RuntimeOperationResult
	initErr       error
	inspectResult service.RuntimeView
	inspectErr    error
}

func (s *runtimeOpServiceStub) Trigger(_ context.Context, _ auth.Principal, _ string, _ service.RuntimeOperation) (service.RuntimeOperationResult, error) {
	return s.triggerResult, s.triggerErr
}

func (s *runtimeOpServiceStub) RequestInitialize(_ context.Context, _ auth.Principal, _ string) (service.RuntimeOperationResult, error) {
	return s.initResult, s.initErr
}

func (s *runtimeOpServiceStub) InspectApp(_ context.Context, _ auth.Principal, _ string) (service.RuntimeView, error) {
	return s.inspectResult, s.inspectErr
}

// newAppRuntimeTestRouter 构建用于测试的 gin router。
func newAppRuntimeTestRouter(t *testing.T, svc runtimeOperationService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterAppRuntimeRoutes(router, NewAppRuntimeHandler(svc))
	return router
}

// TestAppRuntimeStartHappy 验证应用运行时启动成功路径的成功路径场景。
func TestAppRuntimeStartHappy(t *testing.T) {
	stub := &runtimeOpServiceStub{triggerResult: service.RuntimeOperationResult{JobID: "job-1", Operation: service.RuntimeOperationStart}}
	router := newAppRuntimeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/runtime/start", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Contains(t, w.Body.String(), "runtime_operation")
}

// TestAppRuntimeStartForbidden 验证应用运行时启动禁止访问的异常或拒绝路径场景。
func TestAppRuntimeStartForbidden(t *testing.T) {
	stub := &runtimeOpServiceStub{triggerErr: service.ErrRuntimeOperationDenied}
	router := newAppRuntimeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/runtime/start", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestAppRuntimeStartNotFound 验证应用运行时启动未找到的异常或拒绝路径场景。
func TestAppRuntimeStartNotFound(t *testing.T) {
	stub := &runtimeOpServiceStub{triggerErr: service.ErrNotFound}
	router := newAppRuntimeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/missing/runtime/start", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestAppRuntimeGetRuntimeHappy 验证应用运行时获取运行时成功路径的成功路径场景。
func TestAppRuntimeGetRuntimeHappy(t *testing.T) {
	stub := &runtimeOpServiceStub{inspectResult: service.RuntimeView{Status: "running"}}
	router := newAppRuntimeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/runtime", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "runtime")
}

// TestAppRuntimeInitializeHappy 验证应用运行时初始化成功路径的成功路径场景。
func TestAppRuntimeInitializeHappy(t *testing.T) {
	stub := &runtimeOpServiceStub{initResult: service.RuntimeOperationResult{JobID: "job-init"}}
	router := newAppRuntimeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/initialize", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Contains(t, w.Body.String(), "runtime_operation")
}

// TestAppRuntimeInitializeConflict 验证应用运行时初始化冲突的异常或拒绝路径场景。
func TestAppRuntimeInitializeConflict(t *testing.T) {
	stub := &runtimeOpServiceStub{initErr: service.ErrAppNotReinitializable}
	router := newAppRuntimeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/initialize", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}

