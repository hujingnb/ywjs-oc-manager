// Package handlers 的 auth_test 覆盖登录、刷新令牌和当前用户接口的 handler 行为。
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
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
	require.Contains(t, recorder.Body.String(), "缺少必填参数")
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

func newAuthTestRouter(t *testing.T, svc *authServiceStub) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	handler := NewAuthHandler(svc)
	RegisterPublicAuthRoutes(router, handler)
	RegisterAuthMeRoutes(router, handler)
	return router
}

type authServiceStub struct {
	loginResult    service.LoginResult
	loginErr       error
	meResult       service.AuthUser
	lastLoginInput service.LoginInput
	lastPrincipal  auth.Principal
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
