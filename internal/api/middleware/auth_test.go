// Package middleware 的 RequireUserAuth 单元测试。
//
// 测试只构造真实的 *auth.TokenManager 与最小化 gin engine，断言中间件在
// 缺失 / 非法 / 过期 / 合法 token 五大场景下的行为：失败场景统一短路 401
// + ErrorResponse{Code: "UNAUTHENTICATED"}；成功场景把 Principal 写入
// ctx 并执行下游 handler。
package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
)

// TestRequireUserAuth_MissingHeader 覆盖完全缺失 Authorization header 的场景：
// 应短路返回 401 + UNAUTHENTICATED + 中文文案"缺少访问令牌"，下游 handler
// 不执行。这是浏览器未带凭证调接口最常见的入口路径。
func TestRequireUserAuth_MissingHeader(t *testing.T) {
	r, downstream := newTestEngine(t)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assertErrorBody(t, rec.Body.Bytes(), "UNAUTHENTICATED", "缺少访问令牌")
	assert.False(t, downstream.invoked, "下游 handler 不应被执行")
}

// TestRequireUserAuth_NonBearerScheme 覆盖非 Bearer scheme 的场景：
// 调用方用 Basic 认证（或自定义 scheme）时，应识别为缺少访问令牌而非
// 试图解析 token，避免对 Basic credentials 做无谓的 token 校验。
func TestRequireUserAuth_NonBearerScheme(t *testing.T) {
	r, downstream := newTestEngine(t)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assertErrorBody(t, rec.Body.Bytes(), "UNAUTHENTICATED", "缺少访问令牌")
	assert.False(t, downstream.invoked)
}

// TestRequireUserAuth_EmptyBearerToken 覆盖 Bearer 但 token 为空字符串：
// 这通常意味着客户端逻辑 bug（拿到空 token 仍发请求），中间件应作为
// 缺失处理，而不是把空串送进 VerifyAccessToken 暴露内部错误细节。
func TestRequireUserAuth_EmptyBearerToken(t *testing.T) {
	r, downstream := newTestEngine(t)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assertErrorBody(t, rec.Body.Bytes(), "UNAUTHENTICATED", "缺少访问令牌")
	assert.False(t, downstream.invoked)
}

// TestRequireUserAuth_InvalidSignature 覆盖签名被篡改的场景：
// VerifyAccessToken 会返回错误，中间件统一映射为 401 + "访问令牌无效"，
// 不暴露"签名错"这种具体原因（防探测）。
func TestRequireUserAuth_InvalidSignature(t *testing.T) {
	manager := newTestTokenManager(t, time.Minute)
	token, err := manager.SignAccessToken(auth.Principal{UserID: "u-1", Role: "platform_admin"})
	require.NoError(t, err)
	// 修改最后一字符破坏 HMAC 签名。
	tampered := token[:len(token)-1] + "X"

	r, downstream := newTestEngineWithManager(t, manager)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tampered)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assertErrorBody(t, rec.Body.Bytes(), "UNAUTHENTICATED", "访问令牌无效")
	assert.False(t, downstream.invoked)
}

// TestRequireUserAuth_ExpiredToken 覆盖 token 过期场景：
// 用极短 TTL 签发，sleep 后再调用，VerifyAccessToken 应判定过期；
// 中间件同样映射为 401 + "访问令牌无效"，与"签名错"返回同一 code，
// 让前端用统一刷新逻辑处理（拿到 401 后尝试 refresh）。
func TestRequireUserAuth_ExpiredToken(t *testing.T) {
	manager := newTestTokenManager(t, time.Millisecond)
	token, err := manager.SignAccessToken(auth.Principal{UserID: "u-1", Role: "platform_admin"})
	require.NoError(t, err)
	// 确保 token 已越过 exp 时刻；TTL=1ms，sleep 50ms 留足缓冲。
	time.Sleep(50 * time.Millisecond)

	r, downstream := newTestEngineWithManager(t, manager)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assertErrorBody(t, rec.Body.Bytes(), "UNAUTHENTICATED", "访问令牌无效")
	assert.False(t, downstream.invoked)
}

// TestRequireUserAuth_HappyPath 覆盖合法 access token 注入主体的场景：
// 中间件解析、校验通过后必须把 Principal 写入 c.Request.Context()，下游
// handler 通过 auth.PrincipalFromContext 取到字段完全一致的主体。这是
// 整个改造的核心契约，任何回归都会让下游业务取主体失败。
func TestRequireUserAuth_HappyPath(t *testing.T) {
	manager := newTestTokenManager(t, time.Minute)
	want := auth.Principal{UserID: "u-9", OrgID: "org-3", Role: "org_admin"}
	token, err := manager.SignAccessToken(want)
	require.NoError(t, err)

	r, downstream := newTestEngineWithManager(t, manager)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, downstream.invoked, "下游 handler 必须被执行")
	assert.True(t, downstream.principalFound)
	assert.Equal(t, want, downstream.principal)
}

// downstreamProbe 是用于断言下游 handler 是否被执行、是否拿到主体的探针。
// 单测用，不暴露给生产代码。
type downstreamProbe struct {
	invoked        bool
	principalFound bool
	principal      auth.Principal
}

// newTestEngine 构造一台挂载 RequireUserAuth + 探针 handler 的 gin engine，
// 使用默认 1 分钟 TTL 的 TokenManager 供"缺 header / 非 Bearer / 空 token"等
// 不需要签发真实 token 的用例使用。
func newTestEngine(t *testing.T) (*gin.Engine, *downstreamProbe) {
	t.Helper()
	return newTestEngineWithManager(t, newTestTokenManager(t, time.Minute))
}

// newTestEngineWithManager 让调用方注入特定 TokenManager（如短 TTL 用于过期测试），
// 同时返回探针，断言下游执行情况与 ctx 注入。
func newTestEngineWithManager(t *testing.T, manager *auth.TokenManager) (*gin.Engine, *downstreamProbe) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	probe := &downstreamProbe{}
	engine := gin.New()
	engine.Use(RequireUserAuth(manager))
	engine.GET("/protected", func(c *gin.Context) {
		probe.invoked = true
		principal, ok := auth.PrincipalFromContext(c.Request.Context())
		probe.principalFound = ok
		probe.principal = principal
		c.Status(http.StatusOK)
	})
	return engine, probe
}

// newTestTokenManager 构造一个对单测足够可控的 TokenManager：
// 接受自定义 access TTL（短 TTL 用于过期测试），refresh TTL 固定 1h
// 与具体测试无关。
func newTestTokenManager(t *testing.T, accessTTL time.Duration) *auth.TokenManager {
	t.Helper()
	manager, err := auth.NewTokenManager("access-secret", "refresh-secret", accessTTL, time.Hour)
	require.NoError(t, err)
	return manager
}

// assertErrorBody 反序列化 ErrorResponse 并断言 code / message 字段完全匹配。
// 把 JSON 校验集中到这里，避免每条用例重复 unmarshal 样板。
func assertErrorBody(t *testing.T, raw []byte, wantCode, wantMessage string) {
	t.Helper()
	var got apierror.ErrorResponse
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, wantCode, got.Code)
	assert.Equal(t, wantMessage, got.Message)
}
