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

// platformOverviewServiceStub 实现 platformOverviewService 接口。
type platformOverviewServiceStub struct {
	result service.PlatformOverview
	err    error
}

func (s *platformOverviewServiceStub) Get(_ context.Context, _ auth.Principal) (service.PlatformOverview, error) {
	return s.result, s.err
}

// newPlatformOverviewTestRouter 构建用于测试的 gin router。
func newPlatformOverviewTestRouter(t *testing.T, svc platformOverviewService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterPlatformOverviewRoutes(router, NewPlatformOverviewHandler(svc))
	return router
}

// TestPlatformOverviewGetHappy 验证平台概览获取成功路径的成功路径场景。
func TestPlatformOverviewGetHappy(t *testing.T) {
	stub := &platformOverviewServiceStub{result: service.PlatformOverview{OrganizationCount: 3, MemberCount: 10}}
	router := newPlatformOverviewTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/overview", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "overview")
}

// TestPlatformOverviewGetForbidden 验证平台概览获取禁止访问的异常或拒绝路径场景。
func TestPlatformOverviewGetForbidden(t *testing.T) {
	stub := &platformOverviewServiceStub{err: service.ErrForbidden}
	router := newPlatformOverviewTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/overview", nil)
	// 非平台管理员，service 返回 ErrForbidden
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

