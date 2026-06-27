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

// channelServiceStub 实现 channelService 接口，仅 stub 测试用到的方法。
type channelServiceStub struct {
	beginResult  service.ChallengeResult
	beginErr     error
	pollResult   service.ProgressResult
	pollErr      error
	unbindErr    error
	feishuResult service.ChallengeResult
	feishuErr    error
	// beganFeishu 记录 BeginFeishuAuth 是否被调用，用于断言分流路径。
	beganFeishu bool
	// lastFeishuDomain 记录最近一次调用 BeginFeishuAuth 传入的 domain，用于断言请求体解析正确。
	lastFeishuDomain string
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

// BeginFeishuAuth stub 记录调用，用于飞书分流路径测试。
func (s *channelServiceStub) BeginFeishuAuth(_ context.Context, _ auth.Principal, _ string, in service.FeishuAuthInput) (service.ChallengeResult, error) {
	s.beganFeishu = true
	s.lastFeishuDomain = in.Domain
	return s.feishuResult, s.feishuErr
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

// TestChannelsBeginAuthInstanceNotReady 验证实例未就绪（重启 / 升级中）时映射为 409 Conflict。
func TestChannelsBeginAuthInstanceNotReady(t *testing.T) {
	stub := &channelServiceStub{beginErr: service.ErrInstanceNotReady}
	router := newChannelsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/channels/wechat/auth", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "INSTANCE_NOT_READY")
}

// TestChannelsBeginFeishuAuthScan 验证飞书 scan 模式请求体被正确解析并路由到 BeginFeishuAuth，
// 微信路径的 BeginAuth 不被调用（双模式分流隔离）。
func TestChannelsBeginFeishuAuthScan(t *testing.T) {
	stub := &channelServiceStub{feishuResult: service.ChallengeResult{Status: "pending_auth", ChannelType: "feishu", JobID: "j1"}}
	router := newChannelsTestRouter(t, stub)

	w := httptest.NewRecorder()
	body := strings.NewReader(`{"domain":"feishu"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/channels/feishu/auth", body)
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	// 飞书分流到 BeginFeishuAuth，domain 字段正确传递。
	require.True(t, stub.beganFeishu)
	require.Equal(t, "feishu", stub.lastFeishuDomain)
	assert.Contains(t, w.Body.String(), "challenge")
}

// TestChannelsBeginFeishuAuthBadBody 验证飞书请求体 domain 非法（不在 feishu/lark 内）时返回 400，
// 不触发 service 调用。
func TestChannelsBeginFeishuAuthBadBody(t *testing.T) {
	stub := &channelServiceStub{}
	router := newChannelsTestRouter(t, stub)

	w := httptest.NewRecorder()
	// domain 非法（binding 要求 omitempty,oneof=feishu lark），应触发 400。
	body := strings.NewReader(`{"domain":"qq"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/channels/feishu/auth", body)
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	// BeginFeishuAuth 不应被调用。
	assert.False(t, stub.beganFeishu)
}

// TestChannelsBeginWechatUnchanged 验证微信渠道在飞书分流引入后仍走原 BeginAuth 路径，
// 不受影响（回归保护）。
func TestChannelsBeginWechatUnchanged(t *testing.T) {
	stub := &channelServiceStub{beginResult: service.ChallengeResult{Status: "pending_auth", ChannelType: "wechat"}}
	router := newChannelsTestRouter(t, stub)

	w := httptest.NewRecorder()
	// 微信发起请求无需请求体，原路径不变。
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/channels/wechat/auth", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	// 微信路径不触发 BeginFeishuAuth。
	assert.False(t, stub.beganFeishu)
	assert.Contains(t, w.Body.String(), "challenge")
}
