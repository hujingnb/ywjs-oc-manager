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

// channelServiceStub 实现 channelService 接口，仅 stub 测试用到的方法。
type channelServiceStub struct {
	beginResult  service.ChallengeResult
	beginErr     error
	pollResult   service.ProgressResult
	pollErr      error
	unbindErr    error
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

// newChannelsTestRouter 构建用于测试的 gin router + token manager。
func newChannelsTestRouter(t *testing.T, svc channelService) (*gin.Engine, *auth.TokenManager) {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	tokens, err := auth.NewTokenManager("a", "b", time.Minute, time.Hour)
	require.NoError(t, err)
	router := gin.New()
	RegisterChannelRoutes(router, NewChannelsHandler(svc, tokens))
	return router, tokens
}

func TestChannelsBeginAuthHappy(t *testing.T) {
	stub := &channelServiceStub{beginResult: service.ChallengeResult{Status: "pending_auth", ChannelType: "wechat"}}
	router, tokens := newChannelsTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/channels/wechat/auth", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "challenge")
}

func TestChannelsBeginAuthForbidden(t *testing.T) {
	stub := &channelServiceStub{beginErr: service.ErrForbidden}
	router, tokens := newChannelsTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-2/channels/wechat/auth", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestChannelsBeginAuthNotFound(t *testing.T) {
	stub := &channelServiceStub{beginErr: service.ErrNotFound}
	router, tokens := newChannelsTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/missing/channels/wechat/auth", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestChannelsPollAuthHappy(t *testing.T) {
	stub := &channelServiceStub{pollResult: service.ProgressResult{Status: "pending"}}
	router, tokens := newChannelsTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/channels/wechat/auth", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "progress")
}

func TestChannelsUnbindHappy(t *testing.T) {
	stub := &channelServiceStub{}
	router, tokens := newChannelsTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/channels/wechat/unbind", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestChannelsRequiresToken(t *testing.T) {
	stub := &channelServiceStub{}
	router, _ := newChannelsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/channels/wechat/auth", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestChannelsAdapterMissing(t *testing.T) {
	stub := &channelServiceStub{beginErr: service.ErrChannelAdapterMissing}
	router, tokens := newChannelsTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/channels/disabled/auth", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}
