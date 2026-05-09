package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAuthLoginReturnsTokenPair(t *testing.T) {
	router, _ := newAuthTestRouter(t, &authServiceStub{
		loginResult: service.LoginResult{
			User: service.AuthUser{ID: "user-1", Username: "member@example.com"},
			Tokens: service.TokenPair{
				AccessToken:  "access-token",
				RefreshToken: "refresh-token",
			},
		},
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"member@example.com","password":"secret"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response service.LoginResult
	err := json.Unmarshal(recorder.Body.Bytes(), &response)
	require.NoError(t, err)
	require.Equal(t, "refresh-token", response.Tokens.RefreshToken)
}

func TestAuthLoginRejectsInvalidBody(t *testing.T) {
	router, _ := newAuthTestRouter(t, &authServiceStub{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestAuthMeRequiresBearerToken(t *testing.T) {
	router, _ := newAuthTestRouter(t, &authServiceStub{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestAuthMeReturnsCurrentUser(t *testing.T) {
	svc := &authServiceStub{meResult: service.AuthUser{ID: "user-1", Username: "member@example.com"}}
	router, tokens := newAuthTestRouter(t, svc)
	accessToken, err := tokens.SignAccessToken(auth.Principal{UserID: "user-1", Role: "org_member"})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	request.Header.Set("Authorization", "Bearer "+accessToken)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "user-1", svc.lastPrincipal.UserID)
}

func newAuthTestRouter(t *testing.T, svc *authServiceStub) (*gin.Engine, *auth.TokenManager) {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	tokens, err := auth.NewTokenManager("access-secret", "refresh-secret", time.Minute, time.Hour)
	require.NoError(t, err)
	router := gin.New()
	RegisterAuthRoutes(router, NewAuthHandler(svc, tokens))
	return router, tokens
}

type authServiceStub struct {
	loginResult   service.LoginResult
	meResult      service.AuthUser
	lastPrincipal auth.Principal
}

func (s *authServiceStub) Login(_ context.Context, _ service.LoginInput) (service.LoginResult, error) {
	return s.loginResult, nil
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
