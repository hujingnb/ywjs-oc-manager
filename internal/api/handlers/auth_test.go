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

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d, body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var response service.LoginResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Tokens.RefreshToken != "refresh-token" {
		t.Fatalf("refresh token = %q, want refresh-token", response.Tokens.RefreshToken)
	}
}

func TestAuthLoginRejectsInvalidBody(t *testing.T) {
	router, _ := newAuthTestRouter(t, &authServiceStub{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestAuthMeRequiresBearerToken(t *testing.T) {
	router, _ := newAuthTestRouter(t, &authServiceStub{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status code = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestAuthMeReturnsCurrentUser(t *testing.T) {
	svc := &authServiceStub{meResult: service.AuthUser{ID: "user-1", Username: "member@example.com"}}
	router, tokens := newAuthTestRouter(t, svc)
	accessToken, err := tokens.SignAccessToken(auth.Principal{UserID: "user-1", Role: "org_member"})
	if err != nil {
		t.Fatalf("SignAccessToken() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	request.Header.Set("Authorization", "Bearer "+accessToken)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d, body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if svc.lastPrincipal.UserID != "user-1" {
		t.Fatalf("principal = %+v, want user-1", svc.lastPrincipal)
	}
}

func newAuthTestRouter(t *testing.T, svc *authServiceStub) (*gin.Engine, *auth.TokenManager) {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	tokens, err := auth.NewTokenManager("access-secret", "refresh-secret", time.Minute, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
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
