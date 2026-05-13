// Package handlers 的 organizations_test 覆盖组织管理 handler 的鉴权、创建和更新响应语义。
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
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// TestOrganizationsCreateRequiresToken 验证组织创建要求令牌的预期行为场景。
func TestOrganizationsCreateRequiresToken(t *testing.T) {
	router, _ := newOrganizationsTestRouter(t, &organizationServiceStub{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations", bytes.NewBufferString(`{"name":"测试组织","code":"test-org","admin_username":"admin","admin_display_name":"管理员","admin_password":"secret-password","enabled_models":["qwen2.5:7b"]}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

// TestOrganizationsCreateReturnsCreatedOrganization 验证组织创建返回已创建组织的成功路径场景。
func TestOrganizationsCreateReturnsCreatedOrganization(t *testing.T) {
	svc := &organizationServiceStub{
		createResult: service.OrganizationResult{ID: "org-1", Name: "测试组织", Status: domain.StatusActive},
	}
	router, tokens := newOrganizationsTestRouter(t, svc)
	accessToken, err := tokens.SignAccessToken(auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations", bytes.NewBufferString(`{"name":"测试组织","code":"test-org","admin_username":"admin","admin_display_name":"管理员","admin_password":"secret-password","enabled_models":["qwen2.5:7b"]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+accessToken)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	var response struct {
		Organization service.OrganizationResult `json:"organization"`
	}
	err = json.Unmarshal(recorder.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "测试组织", response.Organization.Name)
	assert.Equal(t, domain.UserRolePlatformAdmin, svc.lastPrincipal.Role)
	assert.Equal(t, "test-org", svc.lastCreateInput.Code)
	assert.Equal(t, "admin", svc.lastCreateInput.AdminUsername)
	assert.Equal(t, "管理员", svc.lastCreateInput.AdminDisplayName)
	assert.Equal(t, "secret-password", svc.lastCreateInput.AdminPassword)
	assert.Equal(t, []string{"qwen2.5:7b"}, svc.lastCreateInput.EnabledModels)
}

// TestOrganizationsUpdatePassesEnabledModels 验证组织更新请求会把模型 allowlist 传给 service。
func TestOrganizationsUpdatePassesEnabledModels(t *testing.T) {
	svc := &organizationServiceStub{
		createResult: service.OrganizationResult{ID: "org-1", Name: "测试组织", Status: domain.StatusActive, EnabledModels: []string{"deepseek-r1:14b"}},
	}
	router, tokens := newOrganizationsTestRouter(t, svc)
	accessToken, err := tokens.SignAccessToken(auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/organizations/org-1", bytes.NewBufferString(`{"name":"测试组织","enabled_models":["deepseek-r1:14b"]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+accessToken)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "org-1", svc.lastUpdateOrgID)
	assert.Equal(t, []string{"deepseek-r1:14b"}, svc.lastUpdateInput.EnabledModels)
	assert.True(t, svc.lastUpdateInput.EnabledModelsSet)
}

// TestOrganizationsUpdateKeepsEnabledModelsWhenOmitted 验证更新请求缺省模型字段时不会要求重写 allowlist。
func TestOrganizationsUpdateKeepsEnabledModelsWhenOmitted(t *testing.T) {
	svc := &organizationServiceStub{
		createResult: service.OrganizationResult{ID: "org-1", Name: "测试组织", Status: domain.StatusActive},
	}
	router, tokens := newOrganizationsTestRouter(t, svc)
	accessToken, err := tokens.SignAccessToken(auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/organizations/org-1", bytes.NewBufferString(`{"name":"测试组织"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+accessToken)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "org-1", svc.lastUpdateOrgID)
	assert.False(t, svc.lastUpdateInput.EnabledModelsSet)
	assert.Nil(t, svc.lastUpdateInput.EnabledModels)
}

// TestOrganizationsCreateRequiresAdminFields 验证组织创建要求管理员字段的预期行为场景。
func TestOrganizationsCreateRequiresAdminFields(t *testing.T) {
	router, tokens := newOrganizationsTestRouter(t, &organizationServiceStub{})
	accessToken, err := tokens.SignAccessToken(auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations", bytes.NewBufferString(`{"name":"测试组织"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+accessToken)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "缺少必填参数")
	require.Contains(t, recorder.Body.String(), "admin_username")
}

// TestOrganizationsCreateReturnsBusinessValidationMessage 验证组织创建业务校验失败时返回具体原因的异常路径场景。
func TestOrganizationsCreateReturnsBusinessValidationMessage(t *testing.T) {
	svc := &organizationServiceStub{
		createErr: fmt.Errorf("%w: 组织标识必须为 3-32 位小写字母、数字或短横线，且不能以短横线开头或结尾", service.ErrMemberCreateInvalid),
	}
	router, tokens := newOrganizationsTestRouter(t, svc)
	accessToken, err := tokens.SignAccessToken(auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations", bytes.NewBufferString(`{"name":"aa","code":"aa","admin_username":"admin","admin_display_name":"admin","admin_password":"admin123"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+accessToken)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "组织标识必须")
	require.NotContains(t, recorder.Body.String(), "请求参数不完整")
}

// TestOrganizationsCreateMapsConflict 验证组织创建映射冲突的异常或拒绝路径场景。
func TestOrganizationsCreateMapsConflict(t *testing.T) {
	router, tokens := newOrganizationsTestRouter(t, &organizationServiceStub{createErr: service.ErrConflict})
	accessToken, err := tokens.SignAccessToken(auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations", bytes.NewBufferString(`{"name":"测试组织","code":"test-org","admin_username":"admin","admin_display_name":"管理员","admin_password":"secret-password"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+accessToken)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusConflict, recorder.Code)
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
	createResult    service.OrganizationResult
	createErr       error
	lastPrincipal   auth.Principal
	lastCreateInput service.OrganizationInput
	lastUpdateOrgID string
	lastUpdateInput service.OrganizationInput
}

func (s *organizationServiceStub) CreateOrganization(_ context.Context, principal auth.Principal, input service.OrganizationInput) (service.OrganizationResult, error) {
	s.lastPrincipal = principal
	s.lastCreateInput = input
	if s.createErr != nil {
		return service.OrganizationResult{}, s.createErr
	}
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

func (s *organizationServiceStub) UpdateOrganization(_ context.Context, principal auth.Principal, orgID string, input service.OrganizationInput) (service.OrganizationResult, error) {
	s.lastPrincipal = principal
	s.lastUpdateOrgID = orgID
	s.lastUpdateInput = input
	return s.createResult, nil
}

func (s *organizationServiceStub) SetOrganizationStatus(_ context.Context, principal auth.Principal, _, _ string) (service.OrganizationResult, error) {
	s.lastPrincipal = principal
	return s.createResult, nil
}
