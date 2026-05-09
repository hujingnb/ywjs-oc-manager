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

// platformOverviewServiceStub 实现 platformOverviewService 接口。
type platformOverviewServiceStub struct {
	result service.PlatformOverview
	err    error
}

func (s *platformOverviewServiceStub) Get(_ context.Context, _ auth.Principal) (service.PlatformOverview, error) {
	return s.result, s.err
}

// newPlatformOverviewTestRouter 构建用于测试的 gin router + token manager。
func newPlatformOverviewTestRouter(t *testing.T, svc platformOverviewService) (*gin.Engine, *auth.TokenManager) {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	tokens, err := auth.NewTokenManager("a", "b", time.Minute, time.Hour)
	require.NoError(t, err)
	router := gin.New()
	RegisterPlatformOverviewRoutes(router, NewPlatformOverviewHandler(svc, tokens))
	return router, tokens
}

func TestPlatformOverviewGetHappy(t *testing.T) {
	stub := &platformOverviewServiceStub{result: service.PlatformOverview{OrganizationCount: 3, MemberCount: 10}}
	router, tokens := newPlatformOverviewTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/overview", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "overview")
}

func TestPlatformOverviewGetForbidden(t *testing.T) {
	stub := &platformOverviewServiceStub{err: service.ErrForbidden}
	router, tokens := newPlatformOverviewTestRouter(t, stub)
	// 非平台管理员，service 返回 ErrForbidden
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/overview", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestPlatformOverviewGetRequiresToken(t *testing.T) {
	stub := &platformOverviewServiceStub{}
	router, _ := newPlatformOverviewTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/overview", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
