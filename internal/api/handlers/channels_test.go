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
	// workWechatResult/workWechatErr 是企业微信分流的预置返回值。
	workWechatResult service.ChallengeResult
	workWechatErr    error
	// beganWorkWeChat 记录 BeginWorkWechatAuth 是否被调用，用于断言分流路径。
	beganWorkWeChat bool
	// lastWorkWechatIn 记录最近一次调用 BeginWorkWechatAuth 的入参，用于断言请求体解析正确。
	lastWorkWechatIn service.WorkWechatAuthInput
	// dingtalkResult/dingtalkErr 是钉钉分流的预置返回值。
	dingtalkResult service.ChallengeResult
	dingtalkErr    error
	// lastDingtalkInput 记录最近一次调用 BeginDingtalkAuth 的入参，用于断言请求体解析正确。
	lastDingtalkInput service.DingtalkAuthInput
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

// BeginWorkWechatAuth stub 记录调用，用于企业微信分流路径测试。
func (s *channelServiceStub) BeginWorkWechatAuth(_ context.Context, _ auth.Principal, _ string, in service.WorkWechatAuthInput) (service.ChallengeResult, error) {
	s.beganWorkWeChat = true
	s.lastWorkWechatIn = in
	return s.workWechatResult, s.workWechatErr
}

// BeginDingtalkAuth stub 记录入参并返回预置挑战，供 TestBeginAuth_Dingtalk 断言分流。
func (s *channelServiceStub) BeginDingtalkAuth(_ context.Context, _ auth.Principal, _ string, in service.DingtalkAuthInput) (service.ChallengeResult, error) {
	s.lastDingtalkInput = in
	return s.dingtalkResult, s.dingtalkErr
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

// TestBeginAuth_WorkWeChat 覆盖企业微信手填发起：handler 读 bot_id/secret body → 调 BeginWorkWechatAuth。
func TestBeginAuth_WorkWeChat(t *testing.T) {
	// fake channelService 记录 BeginWorkWechatAuth 入参，预置成功返回值。
	stub := &channelServiceStub{workWechatResult: service.ChallengeResult{Status: "pending_auth", ChannelType: "work_wechat"}}
	router := newChannelsTestRouter(t, stub)

	w := httptest.NewRecorder()
	// POST /api/v1/apps/app-1/channels/work_wechat/auth body={"bot_id":"bot-1","secret":"sec-1"}
	body := strings.NewReader(`{"bot_id":"bot-1","secret":"sec-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/channels/work_wechat/auth", body)
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	// 断言：200；fake 收到 in.BotID=="bot-1" && in.Secret=="sec-1"。
	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, stub.beganWorkWeChat)
	assert.Equal(t, "bot-1", stub.lastWorkWechatIn.BotID)
	assert.Equal(t, "sec-1", stub.lastWorkWechatIn.Secret)
	assert.Contains(t, w.Body.String(), "challenge")
}

// TestBeginAuth_WorkWeChat_BadBody 覆盖企业微信缺字段（bot_id 或 secret 未填）返回 400 BAD_REQUEST。
func TestBeginAuth_WorkWeChat_BadBody(t *testing.T) {
	// body={} 缺必填字段，binding:"required" 应拦截并返回 400。
	stub := &channelServiceStub{}
	router := newChannelsTestRouter(t, stub)

	w := httptest.NewRecorder()
	body := strings.NewReader(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/channels/work_wechat/auth", body)
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	// 缺必填字段应返回 400，且不触发 service 调用。
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "BAD_REQUEST")
	assert.False(t, stub.beganWorkWeChat)
}

// TestBeginAuth_Dingtalk 验证 dingtalk 渠道分流到 BeginDingtalkAuth，正确解析 client_id/client_secret。
func TestBeginAuth_Dingtalk(t *testing.T) {
	// fake channelService 记录 BeginDingtalkAuth 入参，预置成功返回值。
	stub := &channelServiceStub{dingtalkResult: service.ChallengeResult{Status: "pending_auth", ChannelType: "dingtalk"}}
	router := newChannelsTestRouter(t, stub)

	w := httptest.NewRecorder()
	// POST /api/v1/apps/app-1/channels/dingtalk/auth body={"client_id":"ding-key","client_secret":"ding-secret"}
	body := strings.NewReader(`{"client_id":"ding-key","client_secret":"ding-secret"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/channels/dingtalk/auth", body)
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	// 断言：200；stub 收到正确的 ClientID 与 ClientSecret。
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "ding-key", stub.lastDingtalkInput.ClientID)     // 分流入参 ClientID 正确
	assert.Equal(t, "ding-secret", stub.lastDingtalkInput.ClientSecret) // 分流入参 ClientSecret 正确
	assert.Contains(t, w.Body.String(), "challenge")
}

// TestBeginAuth_Dingtalk_BadBody 验证缺必填字段返回 400（binding:"required" 校验）。
func TestBeginAuth_Dingtalk_BadBody(t *testing.T) {
	// 缺 client_secret，binding:"required" 应拦截并返回 400，不触发 service 调用。
	stub := &channelServiceStub{}
	router := newChannelsTestRouter(t, stub)

	w := httptest.NewRecorder()
	body := strings.NewReader(`{"client_id":"ding-key"}`) // 缺 client_secret
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/channels/dingtalk/auth", body)
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	// 缺必填字段应返回 400。
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "BAD_REQUEST")
}
