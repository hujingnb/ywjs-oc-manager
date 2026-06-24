// Package handlers 的 auth_test 覆盖登录、刷新令牌、当前用户接口和 Altcha 出题接口的 handler 行为。
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
	"oc-manager/internal/auth/pow"
	"oc-manager/internal/service"
)

// TestAuthLoginReturnsTokenPair 验证认证登录返回令牌令牌对的成功路径场景。
func TestAuthLoginReturnsTokenPair(t *testing.T) {
	svc := &authServiceStub{
		loginResult: service.LoginResult{
			User: service.AuthUser{ID: "user-1", Username: "member@example.com"},
			Tokens: service.TokenPair{
				AccessToken:  "access-token",
				RefreshToken: "refresh-token",
			},
		},
	}
	router := newAuthTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"org_code":"test-org","username":"member@example.com","password":"secret"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response service.LoginResult
	err := json.Unmarshal(recorder.Body.Bytes(), &response)
	require.NoError(t, err)
	require.Equal(t, "refresh-token", response.Tokens.RefreshToken)
	require.Equal(t, "test-org", svc.lastLoginInput.OrgCode)
}

// TestAuthLoginRejectsInvalidBody 验证认证登录拒绝非法请求体的异常或拒绝路径场景。
func TestAuthLoginRejectsInvalidBody(t *testing.T) {
	router := newAuthTestRouter(t, &authServiceStub{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	// 绑定错误统一返回 BAD_REQUEST code，文案随 locale 走 catalog（测试路由无 locale 中间件，
	// 回落 en），故断言稳定 code 与缺失字段名而非具体中文文案。
	require.Contains(t, recorder.Body.String(), "BAD_REQUEST")
	require.Contains(t, recorder.Body.String(), "username")
	require.Contains(t, recorder.Body.String(), "password")
}

// TestAuthLoginInvalidCredentialsMentionsOrgCode 验证登录凭证错误时提示组织标识遗漏这一常见误填路径。
func TestAuthLoginInvalidCredentialsMentionsOrgCode(t *testing.T) {
	router := newAuthTestRouter(t, &authServiceStub{loginErr: service.ErrInvalidCredentials})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"member@example.com","password":"wrong"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Contains(t, recorder.Body.String(), "用户名或密码错误")
	require.Contains(t, recorder.Body.String(), "组织标识")
}

// TestAuthMeReturnsCurrentUser 验证认证当前用户接口返回当前用户的成功路径场景。
func TestAuthMeReturnsCurrentUser(t *testing.T) {
	svc := &authServiceStub{meResult: service.AuthUser{ID: "user-1", Username: "member@example.com"}}
	router := newAuthTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: "org_member"})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "user-1", svc.lastPrincipal.UserID)
}

// TestAuthChangePasswordReturnsNoContent 验证已认证改密接口成功时返回 204。
func TestAuthChangePasswordReturnsNoContent(t *testing.T) {
	svc := &authServiceStub{}
	router := newAuthTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", bytes.NewBufferString(`{"old_password":"old-pass","new_password":"new-pass-123"}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: "org_member", OrgID: "org-1"})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNoContent, recorder.Code)
	require.Equal(t, "user-1", svc.lastPrincipal.UserID)
	require.Equal(t, "old-pass", svc.lastChangePasswordInput.OldPassword)
	require.Equal(t, "new-pass-123", svc.lastChangePasswordInput.NewPassword)
}

// TestAuthChangePasswordRejectsMissingFields 验证改密请求缺少必填字段时返回 400 和字段名。
func TestAuthChangePasswordRejectsMissingFields(t *testing.T) {
	router := newAuthTestRouter(t, &authServiceStub{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: "org_member", OrgID: "org-1"})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "old_password")
	require.Contains(t, recorder.Body.String(), "new_password")
}

// TestAuthChangePasswordMapsWrongPasswordToUnauthorized 验证旧密码错误时沿用认证失败响应。
func TestAuthChangePasswordMapsWrongPasswordToUnauthorized(t *testing.T) {
	router := newAuthTestRouter(t, &authServiceStub{changePasswordErr: service.ErrInvalidCredentials})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", bytes.NewBufferString(`{"old_password":"bad-pass","new_password":"new-pass-123"}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: "org_member", OrgID: "org-1"})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Contains(t, recorder.Body.String(), "用户名或密码错误")
}

// TestAuthChangePasswordMapsInvalidNewPasswordToBadRequest 验证新密码业务校验错误返回 400。
func TestAuthChangePasswordMapsInvalidNewPasswordToBadRequest(t *testing.T) {
	router := newAuthTestRouter(t, &authServiceStub{
		changePasswordErr: fmt.Errorf("%w: 新密码至少 8 位", service.ErrMemberCreateInvalid),
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", bytes.NewBufferString(`{"old_password":"old-pass","new_password":"short"}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: "org_member", OrgID: "org-1"})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "新密码至少 8 位")
}

// TestUpdateLocaleReturnsNoContent 验证合法 locale 时 PATCH /auth/me/locale 返回 204。
func TestUpdateLocaleReturnsNoContent(t *testing.T) {
	svc := &authServiceStub{}
	router := newAuthTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/auth/me/locale", bytes.NewBufferString(`{"locale":"zh"}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: "org_member", OrgID: "org-1"})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNoContent, recorder.Code)
	assert.Equal(t, "zh", svc.lastLocale)
}

// TestUpdateLocaleRejectsInvalidLocale 验证 service 返回 ErrInvalidLocale 时 handler 响应 400 INVALID_LOCALE。
func TestUpdateLocaleRejectsInvalidLocale(t *testing.T) {
	router := newAuthTestRouter(t, &authServiceStub{updateLocaleErr: service.ErrInvalidLocale})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/auth/me/locale", bytes.NewBufferString(`{"locale":"fr"}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: "org_member", OrgID: "org-1"})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "INVALID_LOCALE")
}

// TestUpdateLocaleRejectsMissingLocaleField 验证请求体缺少 locale 字段时返回 400。
func TestUpdateLocaleRejectsMissingLocaleField(t *testing.T) {
	router := newAuthTestRouter(t, &authServiceStub{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/auth/me/locale", bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: "org_member", OrgID: "org-1"})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "locale")
}

func newAuthTestRouter(t *testing.T, svc *authServiceStub) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	// captcha=nil 表示验证码关闭，已有测试路径不涉及验证码逻辑。
	handler := NewAuthHandler(svc, nil)
	RegisterPublicAuthRoutes(router, handler)
	RegisterAuthMeRoutes(router, handler)
	return router
}

// 验证码关闭（captcha=nil）时出题接口返回 204。
func TestAltchaChallengeDisabledReturns204(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewAuthHandler(nil, nil) // service 在本路由用不到，captcha=nil
	r := gin.New()
	r.GET("/api/v1/auth/altcha-challenge", h.AltchaChallenge)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/altcha-challenge", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Contains(t, w.Header().Get("Cache-Control"), "no-store")
}

// 验证码开启时出题接口返回 200 且响应体含 challenge/signature 字段。
func TestAltchaChallengeEnabledReturnsChallenge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	captcha := service.NewCaptchaService(pow.NewVerifier("test-secret", 1000, time.Minute), nil)
	h := NewAuthHandler(nil, captcha)
	r := gin.New()
	r.GET("/api/v1/auth/altcha-challenge", h.AltchaChallenge)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/altcha-challenge", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Cache-Control"), "no-store")
	assert.Contains(t, w.Body.String(), "challenge")
	assert.Contains(t, w.Body.String(), "signature")
}

type authServiceStub struct {
	loginResult             service.LoginResult
	loginErr                error
	meResult                service.AuthUser
	changePasswordErr       error
	updateLocaleErr         error
	lastLoginInput          service.LoginInput
	lastPrincipal           auth.Principal
	lastChangePasswordInput service.ChangePasswordInput
	// lastLocale 记录最后一次 UpdateLocale 调用传入的 locale 参数。
	lastLocale string
}

func (s *authServiceStub) Login(_ context.Context, input service.LoginInput) (service.LoginResult, error) {
	s.lastLoginInput = input
	return s.loginResult, s.loginErr
}

func (s *authServiceStub) Refresh(_ context.Context, _ string) (service.LoginResult, error) {
	return s.loginResult, nil
}

func (s *authServiceStub) Logout(_ context.Context, _ string) error {
	return nil
}

func (s *authServiceStub) Me(_ context.Context, principal auth.Principal) (service.AuthUser, error) {
	s.lastPrincipal = principal
	return s.meResult, nil
}

func (s *authServiceStub) ChangePassword(_ context.Context, principal auth.Principal, input service.ChangePasswordInput) error {
	s.lastPrincipal = principal
	s.lastChangePasswordInput = input
	return s.changePasswordErr
}

// UpdateLocale 记录调用入参并按预置错误返回，供 handler 测试断言。
func (s *authServiceStub) UpdateLocale(_ context.Context, _ string, locale string) error {
	s.lastLocale = locale
	return s.updateLocaleErr
}
