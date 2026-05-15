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

// channelServiceStub 实现 channelService 接口，仅 stub 测试用到的方法。
type channelServiceStub struct {
	beginResult service.ChallengeResult
	beginErr    error
	pollResult  service.ProgressResult
	pollErr     error
	unbindErr   error
}

func (s *channelServiceStub) BeginAuth(_ context.Context, _ auth.Principal, _, _ string) (service.ChallengeResult, error) {
	return s.beginResult, s.beginErr
}

func (s *channelServiceStub) PollAuth(_ context.Context, _ auth.Principal, _, _ string) (service.ProgressResult, error) {
	return s.pollResult, s.pollErr
}

func (s *channelServiceStub) Unbind(_ context.Context, _ auth.Principal, _, _ string) error {
	return s.unbindErr
}

// newChannelsTestRouter 构建用于测试的 gin router。
func newChannelsTestRouter(t *testing.T, svc channelService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterChannelRoutes(router, NewChannelsHandler(svc))
	return router
}

// TestChannelsBeginAuthHappy 验证渠道开始认证成功路径的成功路径场景。
func TestChannelsBeginAuthHappy(t *testing.T) {
	stub := &channelServiceStub{beginResult: service.ChallengeResult{Status: "pending_auth", ChannelType: "wechat"}}
	router := newChannelsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/channels/wechat/auth", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "challenge")
}

// TestChannelsBeginAuthForbidden 验证渠道开始认证禁止访问的异常或拒绝路径场景。
func TestChannelsBeginAuthForbidden(t *testing.T) {
	stub := &channelServiceStub{beginErr: service.ErrForbidden}
	router := newChannelsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-2/channels/wechat/auth", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestChannelsBeginAuthNotFound 验证渠道开始认证未找到的异常或拒绝路径场景。
func TestChannelsBeginAuthNotFound(t *testing.T) {
	stub := &channelServiceStub{beginErr: service.ErrNotFound}
	router := newChannelsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/missing/channels/wechat/auth", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestChannelsPollAuthHappy 验证渠道轮询认证成功路径的成功路径场景。
func TestChannelsPollAuthHappy(t *testing.T) {
	stub := &channelServiceStub{pollResult: service.ProgressResult{Status: "pending"}}
	router := newChannelsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/channels/wechat/auth", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "progress")
}

// TestChannelsUnbindHappy 验证渠道解绑成功路径的成功路径场景。
func TestChannelsUnbindHappy(t *testing.T) {
	stub := &channelServiceStub{}
	router := newChannelsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/channels/wechat/unbind", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

// TestChannelsAdapterMissing 验证渠道适配器缺失的异常或拒绝路径场景。
func TestChannelsAdapterMissing(t *testing.T) {
	stub := &channelServiceStub{beginErr: service.ErrChannelAdapterMissing}
	router := newChannelsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/channels/disabled/auth", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}
