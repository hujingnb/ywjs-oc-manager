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
}

// TestOrganizationsUpdateIgnoresModelID 验证组织更新请求即使携带旧的 model_id 字段也能正常处理，
// 该字段已从 DTO 移除，gin 会静默忽略未知字段，服务调用正常完成。
func TestOrganizationsUpdateIgnoresModelID(t *testing.T) {
	svc := &organizationServiceStub{
		createResult: service.OrganizationResult{ID: "org-1", Name: "测试组织", Status: domain.StatusActive},
	}
	router := newOrganizationsTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	// 请求体携带已废弃的 model_id 字段，handler 应正常返回 200，忽略未知字段。
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/organizations/org-1", bytes.NewBufferString(`{"name":"测试组织","model_id":"deepseek-r1:14b"}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "org-1", svc.lastUpdateOrgID)
}

// TestOrganizationsCreateForwardsAssistantVersionIDs 验证组织创建请求把助手版本 allowlist 传给 service。
func TestOrganizationsCreateForwardsAssistantVersionIDs(t *testing.T) {
	svc := &organizationServiceStub{
		createResult: service.OrganizationResult{ID: "org-2", Name: "版本测试组织", Status: domain.StatusActive},
	}
	router := newOrganizationsTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	// 携带两个助手版本 id，验证 handler 正确透传给 service 入参。
	body := `{"name":"版本测试组织","code":"version-org","admin_username":"admin","admin_display_name":"管理员","admin_password":"secret","assistant_version_ids":["v-id-1","v-id-2"]}`
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

// TestOrganizationsCreateForwardsKnowledgeQuotaBytes 验证创建企业时透传企业知识库容量上限。
func TestOrganizationsCreateForwardsKnowledgeQuotaBytes(t *testing.T) {
	svc := &organizationServiceStub{
		createResult: service.OrganizationResult{ID: "org-1", Name: "测试组织", Status: domain.StatusActive},
	}
	router := newOrganizationsTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	// 创建请求显式提交 knowledge_quota_bytes，handler 需要保留指针语义并交给 service 做默认值与合法性校验。
	body := `{"name":"测试组织","code":"test-org","knowledge_quota_bytes":2147483648,"admin_username":"admin","admin_display_name":"管理员","admin_password":"secret-password"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.NotNil(t, svc.lastCreateInput.KnowledgeQuotaBytes)
	assert.Equal(t, int64(2147483648), *svc.lastCreateInput.KnowledgeQuotaBytes)
}

// TestOrganizationsUpdateForwardsKnowledgeQuotaBytes 验证编辑企业时透传企业知识库容量上限。
func TestOrganizationsUpdateForwardsKnowledgeQuotaBytes(t *testing.T) {
	svc := &organizationServiceStub{
		createResult: service.OrganizationResult{ID: "org-1", Name: "测试组织", Status: domain.StatusActive},
	}
	router := newOrganizationsTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	// 更新请求显式提交 knowledge_quota_bytes，nil 与非 nil 的差异由 service 决定是否保留旧值。
	body := `{"name":"测试组织","knowledge_quota_bytes":3221225472}`
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/organizations/org-1", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, svc.lastUpdateInput.KnowledgeQuotaBytes)
	assert.Equal(t, int64(3221225472), *svc.lastUpdateInput.KnowledgeQuotaBytes)
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
}

// TestOrganizationsHandlerUpdateAICCConfig 覆盖正常路径：平台管理员通过 PATCH /organizations/:orgId/aicc-config 开通 AICC。
func TestOrganizationsHandlerUpdateAICCConfig(t *testing.T) {
	limit := int32(5)
	svc := &organizationServiceStub{
		updateAICCConfigResult: service.OrganizationResult{ID: "org-1", Name: "测试组织", Status: domain.StatusActive, AICCEnabled: true, AICCAgentLimit: &limit},
	}
	router := newOrganizationsTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/organizations/org-1/aicc-config", bytes.NewBufferString(`{"enabled":true,"agent_limit":5}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "org-1", svc.lastAICCConfigOrgID)
	assert.True(t, svc.lastAICCConfigInput.Enabled)
	require.NotNil(t, svc.lastAICCConfigInput.AgentLimit)
	assert.Equal(t, int32(5), *svc.lastAICCConfigInput.AgentLimit)
}

// TestOrganizationsHandlerUpdateAICCConfigAllowsDisable 覆盖正常路径：显式 enabled=false 应合法透传，不能被必填校验误拒绝。
func TestOrganizationsHandlerUpdateAICCConfigAllowsDisable(t *testing.T) {
	svc := &organizationServiceStub{
		updateAICCConfigResult: service.OrganizationResult{ID: "org-1", Name: "测试组织", Status: domain.StatusActive, AICCEnabled: false},
	}
	router := newOrganizationsTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/organizations/org-1/aicc-config", bytes.NewBufferString(`{"enabled":false,"agent_limit":null}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.False(t, svc.lastAICCConfigInput.Enabled)
	assert.Nil(t, svc.lastAICCConfigInput.AgentLimit)
}

// TestOrganizationsHandlerUpdateAICCConfigRequiresEnabled 覆盖异常路径：缺省 enabled 必须返回 400，避免空请求体误关闭企业 AICC。
func TestOrganizationsHandlerUpdateAICCConfigRequiresEnabled(t *testing.T) {
	router := newOrganizationsTestRouter(t, &organizationServiceStub{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/organizations/org-1/aicc-config", bytes.NewBufferString(`{"agent_limit":5}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "BAD_REQUEST")
}

// TestOrganizationsHandlerUpdateAICCConfigBadJSON 覆盖异常路径：非法 JSON 返回 400，避免空请求体误关闭企业 AICC。
func TestOrganizationsHandlerUpdateAICCConfigBadJSON(t *testing.T) {
	router := newOrganizationsTestRouter(t, &organizationServiceStub{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/organizations/org-1/aicc-config", bytes.NewBufferString(`{"enabled":true`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "BAD_REQUEST")
}

// TestOrganizationsHandlerUpdateAICCConfigMapsServiceErrors 覆盖新路由的 service sentinel 错误映射。
func TestOrganizationsHandlerUpdateAICCConfigMapsServiceErrors(t *testing.T) {
	cases := []struct {
		name string // 子场景说明
		err  error  // service 返回的错误
		code int    // 期望 HTTP 状态码
	}{
		{name: "无权限映射为 403", err: service.ErrForbidden, code: http.StatusForbidden},                                      // 场景：非平台管理员调用 service 后被拒绝。
		{name: "企业不存在映射为 404", err: service.ErrNotFound, code: http.StatusNotFound},                                      // 场景：目标企业不存在。
		{name: "业务参数错误映射为 400", err: fmt.Errorf("%w: 上限不能为负数", service.ErrInvalidArgument), code: http.StatusBadRequest}, // 场景：上限通过 HTTP 绑定但违反业务边界。
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &organizationServiceStub{updateAICCConfigErr: tc.err}
			router := newOrganizationsTestRouter(t, svc)

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPatch, "/api/v1/organizations/org-1/aicc-config", bytes.NewBufferString(`{"enabled":true}`))
			request.Header.Set("Content-Type", "application/json")
			request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
			router.ServeHTTP(recorder, request)

			require.Equal(t, tc.code, recorder.Code)
		})
	}
}

// TestOrganizationsCreateRequiredFields 验证组织创建仅需必填字段即可成功，
// model_id 字段已从 DTO 移除，请求体不应包含它。
func TestOrganizationsCreateRequiredFields(t *testing.T) {
	svc := &organizationServiceStub{
		createResult: service.OrganizationResult{ID: "org-99", Name: "测试组织2", Status: domain.StatusActive},
	}
	router := newOrganizationsTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	// 请求体仅携带必填字段，验证请求可正常处理并返回 201。
	body := `{"name":"测试组织2","code":"test-org-2","admin_username":"admin","admin_display_name":"管理员","admin_password":"secret-password"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	// 仅必填字段应成功到达 service 并返回 201。
	require.Equal(t, http.StatusCreated, recorder.Code)
	assert.Equal(t, "test-org-2", svc.lastCreateInput.Code)
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
	// 绑定错误统一返回 BAD_REQUEST code，文案随 locale 走 catalog（测试路由无 locale 中间件，
	// 回落 en），故断言稳定 code 与缺失字段名而非具体中文文案。
	require.Contains(t, recorder.Body.String(), "BAD_REQUEST")
	require.Contains(t, recorder.Body.String(), "admin_username")
}

// TestOrganizationsCreateReturnsBusinessValidationMessage 验证组织创建业务校验失败时返回具体原因的异常路径场景。
func TestOrganizationsCreateReturnsBusinessValidationMessage(t *testing.T) {
	svc := &organizationServiceStub{
		createErr: fmt.Errorf("%w: 企业标识必须为 3-32 位小写字母、数字或短横线，且不能以短横线开头或结尾", service.ErrMemberCreateInvalid),
	}
	router := newOrganizationsTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations", bytes.NewBufferString(`{"name":"aa","code":"aa","admin_username":"admin","admin_display_name":"admin","admin_password":"admin123","model_id":"qwen2.5:7b"}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "企业标识必须")
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
	createResult           service.OrganizationResult
	createErr              error
	updateAICCConfigResult service.OrganizationResult
	updateAICCConfigErr    error
	lastPrincipal          auth.Principal
	lastCreateInput        service.OrganizationInput
	lastUpdateOrgID        string
	lastUpdateInput        service.OrganizationInput
	lastAICCConfigOrgID    string
	lastAICCConfigInput    service.AICCConfigInput
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

func (s *organizationServiceStub) UpdateAICCConfig(_ context.Context, principal auth.Principal, orgID string, input service.AICCConfigInput) (service.OrganizationResult, error) {
	s.lastPrincipal = principal
	s.lastAICCConfigOrgID = orgID
	s.lastAICCConfigInput = input
	if s.updateAICCConfigErr != nil {
		return service.OrganizationResult{}, s.updateAICCConfigErr
	}
	if s.updateAICCConfigResult.ID != "" {
		return s.updateAICCConfigResult, nil
	}
	return s.createResult, nil
}
