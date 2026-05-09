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
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOrganizationsCreateRequiresToken(t *testing.T) {
	router, _ := newOrganizationsTestRouter(t, &organizationServiceStub{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations", bytes.NewBufferString(`{"name":"测试组织"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestOrganizationsCreateReturnsCreatedOrganization(t *testing.T) {
	svc := &organizationServiceStub{
		createResult: service.OrganizationResult{ID: "org-1", Name: "测试组织", Status: domain.StatusActive},
	}
	router, tokens := newOrganizationsTestRouter(t, svc)
	accessToken, err := tokens.SignAccessToken(auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations", bytes.NewBufferString(`{"name":"测试组织"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+accessToken)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	var response struct {
		Organization service.OrganizationResult `json:"organization"`
	}
	err = json.Unmarshal(recorder.Body.Bytes(), &response)
	require.NoError(t, err)
	if response.Organization.Name != "测试组织" || svc.lastPrincipal.Role != domain.UserRolePlatformAdmin {
		t.Fatalf("response=%+v principal=%+v", response, svc.lastPrincipal)
	}
}

func newOrganizationsTestRouter(t *testing.T, svc *organizationServiceStub) (*gin.Engine, *auth.TokenManager) {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	tokens, err := auth.NewTokenManager("access-secret", "refresh-secret", time.Minute, time.Hour)
	require.NoError(t, err)
	router := gin.New()
	RegisterOrganizationRoutes(router, NewOrganizationsHandler(svc, tokens))
	return router, tokens
}

type organizationServiceStub struct {
	createResult  service.OrganizationResult
	lastPrincipal auth.Principal
}

func (s *organizationServiceStub) CreateOrganization(_ context.Context, principal auth.Principal, _ service.OrganizationInput) (service.OrganizationResult, error) {
	s.lastPrincipal = principal
	return s.createResult, nil
}

func (s *organizationServiceStub) ListOrganizations(_ context.Context, principal auth.Principal, _, _ int32) ([]service.OrganizationResult, error) {
	s.lastPrincipal = principal
	return []service.OrganizationResult{s.createResult}, nil
}

func (s *organizationServiceStub) GetOrganization(_ context.Context, principal auth.Principal, _ string) (service.OrganizationResult, error) {
	s.lastPrincipal = principal
	return s.createResult, nil
}

func (s *organizationServiceStub) UpdateOrganization(_ context.Context, principal auth.Principal, _ string, _ service.OrganizationInput) (service.OrganizationResult, error) {
	s.lastPrincipal = principal
	return s.createResult, nil
}

func (s *organizationServiceStub) SetOrganizationStatus(_ context.Context, principal auth.Principal, _, _ string) (service.OrganizationResult, error) {
	s.lastPrincipal = principal
	return s.createResult, nil
}
