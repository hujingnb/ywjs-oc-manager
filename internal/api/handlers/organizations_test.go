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

	"github.com/gin-gonic/gin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// TestOrganizationsCreateReturnsCreatedOrganization 验证组织创建返回已创建组织的成功路径场景。
func TestOrganizationsCreateReturnsCreatedOrganization(t *testing.T) {
	svc := &organizationServiceStub{
		createResult: service.OrganizationResult{ID: "org-1", Name: "测试组织", Status: domain.StatusActive},
	}
	router := newOrganizationsTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations", bytes.NewBufferString(`{"name":"测试组织","code":"test-org","admin_username":"admin","admin_display_name":"管理员","admin_password":"secret-password","model_id":"qwen2.5:7b"}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	var response struct {
		Organization service.OrganizationResult `json:"organization"`
	}
	err := json.Unmarshal(recorder.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "测试组织", response.Organization.Name)
	assert.Equal(t, domain.UserRolePlatformAdmin, svc.lastPrincipal.Role)
	assert.Equal(t, "test-org", svc.lastCreateInput.Code)
	assert.Equal(t, "admin", svc.lastCreateInput.AdminUsername)
	assert.Equal(t, "管理员", svc.lastCreateInput.AdminDisplayName)
	assert.Equal(t, "secret-password", svc.lastCreateInput.AdminPassword)
	assert.Equal(t, "qwen2.5:7b", svc.lastCreateInput.ModelID)
}

// TestOrganizationsUpdatePassesModelID 验证组织更新请求会把单模型 ID 传给 service。
func TestOrganizationsUpdatePassesModelID(t *testing.T) {
	svc := &organizationServiceStub{
		createResult: service.OrganizationResult{ID: "org-1", Name: "测试组织", Status: domain.StatusActive, ModelID: "deepseek-r1:14b"},
	}
	router := newOrganizationsTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/organizations/org-1", bytes.NewBufferString(`{"name":"测试组织","model_id":"deepseek-r1:14b"}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "org-1", svc.lastUpdateOrgID)
	assert.Equal(t, "deepseek-r1:14b", svc.lastUpdateInput.ModelID)
	assert.True(t, svc.lastUpdateInput.ModelIDSet)
}

// TestOrganizationsCreateForwardsAssistantVersionIDs 验证组织创建请求把助手版本 allowlist 传给 service。
func TestOrganizationsCreateForwardsAssistantVersionIDs(t *testing.T) {
	svc := &organizationServiceStub{
		createResult: service.OrganizationResult{ID: "org-2", Name: "版本测试组织", Status: domain.StatusActive},
	}
	router := newOrganizationsTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	// 携带两个助手版本 id，验证 handler 正确透传给 service 入参。
	body := `{"name":"版本测试组织","code":"version-org","admin_username":"admin","admin_display_name":"管理员","admin_password":"secret","model_id":"qwen2.5:7b","assistant_version_ids":["v-id-1","v-id-2"]}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	// 确认 allowlist 字段被完整传入 service 入参。
	require.Equal(t, []string{"v-id-1", "v-id-2"}, svc.lastCreateInput.AssistantVersionIDs)
}

// TestOrganizationsUpdateForwardsAssistantVersionIDs 验证组织更新请求把助手版本 allowlist 传给 service 并标记为已设置。
func TestOrganizationsUpdateForwardsAssistantVersionIDs(t *testing.T) {
	svc := &organizationServiceStub{
		createResult: service.OrganizationResult{ID: "org-1", Name: "版本测试组织", Status: domain.StatusActive},
	}
	router := newOrganizationsTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	// 更新时携带单个助手版本 id，验证 handler 设置 AssistantVersionIDsSet = true。
	body := `{"name":"版本测试组织","assistant_version_ids":["v-id-3"]}`
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/organizations/org-1", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	// 确认 allowlist 字段被传入 service，且 AssistantVersionIDsSet 为 true 以触发 service 层更新逻辑。
	require.Equal(t, []string{"v-id-3"}, svc.lastUpdateInput.AssistantVersionIDs)
	require.True(t, svc.lastUpdateInput.AssistantVersionIDsSet)
}

// TestOrganizationsUpdateKeepsModelWhenOmitted 验证更新请求缺省模型字段时不会要求重写模型。
func TestOrganizationsUpdateKeepsModelWhenOmitted(t *testing.T) {
	svc := &organizationServiceStub{
		createResult: service.OrganizationResult{ID: "org-1", Name: "测试组织", Status: domain.StatusActive},
	}
	router := newOrganizationsTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/organizations/org-1", bytes.NewBufferString(`{"name":"测试组织"}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "org-1", svc.lastUpdateOrgID)
	assert.False(t, svc.lastUpdateInput.ModelIDSet)
	assert.Empty(t, svc.lastUpdateInput.ModelID)
}

// TestOrganizationsCreateAllowsMissingModelID 回归测试：组织创建请求不携带 model_id 时应正常通过 gin
// binding 校验并返回 201，防止 CreateOrganizationRequest.ModelID 被再次错误地标注为
// binding:"required" 而导致前端 Phase 3 表单（不发送 model_id）的请求全部被 400 拒绝。
func TestOrganizationsCreateAllowsMissingModelID(t *testing.T) {
	svc := &organizationServiceStub{
		createResult: service.OrganizationResult{ID: "org-99", Name: "无模型组织", Status: domain.StatusActive},
	}
	router := newOrganizationsTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	// 请求体故意不含 model_id，只携带创建组织所必须的其他字段。
	body := `{"name":"无模型组织","code":"no-model-org","admin_username":"admin","admin_display_name":"管理员","admin_password":"secret-password"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	// 缺少 model_id 不应触发 400；请求应成功到达 service 并返回 201。
	require.Equal(t, http.StatusCreated, recorder.Code)
	assert.Empty(t, svc.lastCreateInput.ModelID)
}

// TestOrganizationsCreateRequiresAdminFields 验证组织创建要求管理员字段的预期行为场景。
func TestOrganizationsCreateRequiresAdminFields(t *testing.T) {
	router := newOrganizationsTestRouter(t, &organizationServiceStub{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations", bytes.NewBufferString(`{"name":"测试组织"}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
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
	router := newOrganizationsTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations", bytes.NewBufferString(`{"name":"aa","code":"aa","admin_username":"admin","admin_display_name":"admin","admin_password":"admin123","model_id":"qwen2.5:7b"}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "组织标识必须")
	require.NotContains(t, recorder.Body.String(), "请求参数不完整")
}

// TestOrganizationsCreateMapsConflict 验证组织创建映射冲突的异常或拒绝路径场景。
func TestOrganizationsCreateMapsConflict(t *testing.T) {
	router := newOrganizationsTestRouter(t, &organizationServiceStub{createErr: service.ErrConflict})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations", bytes.NewBufferString(`{"name":"测试组织","code":"test-org","admin_username":"admin","admin_display_name":"管理员","admin_password":"secret-password","model_id":"qwen2.5:7b"}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusConflict, recorder.Code)
}

func newOrganizationsTestRouter(t *testing.T, svc *organizationServiceStub) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterOrganizationRoutes(router, NewOrganizationsHandler(svc))
	return router
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
