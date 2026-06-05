package handlers

import (
	"bytes"
	"context"
	"encoding/json"
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

// avServiceStub 是 assistantVersionService 接口的内存桩。
type avServiceStub struct {
	list        []service.AssistantVersionResult
	one         service.AssistantVersionResult
	err         error
	images      []service.RuntimeImageOption
	createInput service.AssistantVersionInput
	updateInput service.AssistantVersionInput
	updateID    string
}

func (s *avServiceStub) List(context.Context, auth.Principal) ([]service.AssistantVersionResult, error) {
	return s.list, s.err
}
func (s *avServiceStub) Get(context.Context, auth.Principal, string) (service.AssistantVersionResult, error) {
	return s.one, s.err
}
func (s *avServiceStub) Create(_ context.Context, _ auth.Principal, in service.AssistantVersionInput) (service.AssistantVersionResult, error) {
	s.createInput = in
	return s.one, s.err
}
func (s *avServiceStub) Update(_ context.Context, _ auth.Principal, id string, in service.AssistantVersionInput) (service.AssistantVersionResult, error) {
	s.updateID = id
	s.updateInput = in
	return s.one, s.err
}
func (s *avServiceStub) Delete(context.Context, auth.Principal, string) error { return s.err }
func (s *avServiceStub) AddSkillFromLibrary(context.Context, auth.Principal, string, service.AddSkillFromLibraryInput) (service.AssistantVersionResult, error) {
	return s.one, s.err
}
func (s *avServiceStub) DeleteSkill(context.Context, auth.Principal, string, string) (service.AssistantVersionResult, error) {
	return s.one, s.err
}
func (s *avServiceStub) ListRuntimeImages(context.Context, auth.Principal) ([]service.RuntimeImageOption, error) {
	return s.images, s.err
}

func newAVTestRouter(t *testing.T, svc assistantVersionService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterAssistantVersionRoutes(router, NewAssistantVersionsHandler(svc))
	return router
}

// TestAVListReturnsVersions 验证平台管理员可列出版本。
func TestAVListReturnsVersions(t *testing.T) {
	svc := &avServiceStub{list: []service.AssistantVersionResult{{ID: "v1", Name: "标准版"}}}
	router := newAVTestRouter(t, svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/assistant-versions", nil)
	req = withPrincipal(req, auth.Principal{Role: domain.UserRolePlatformAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), "标准版")
}

// TestAVCreateReturns201 验证创建版本返回 201。
func TestAVCreateReturns201(t *testing.T) {
	svc := &avServiceStub{one: service.AssistantVersionResult{ID: "v1", Name: "标准版"}}
	router := newAVTestRouter(t, svc)
	body, _ := json.Marshal(CreateAssistantVersionRequest{
		Name: "标准版", SystemPrompt: "p", ImageID: "v2026.5.16", MainModel: "qwen",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/assistant-versions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{Role: domain.UserRolePlatformAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusCreated, resp.Code)
}

// TestAVCreatePassesIndustryKnowledgeBaseIDs 验证创建请求会透传行业知识库关联 ID 列表。
func TestAVCreatePassesIndustryKnowledgeBaseIDs(t *testing.T) {
	svc := &avServiceStub{one: service.AssistantVersionResult{ID: "v1", Name: "标准版"}}
	router := newAVTestRouter(t, svc)
	body, err := json.Marshal(CreateAssistantVersionRequest{
		Name: "标准版", SystemPrompt: "p", ImageID: "v2026.5.16", MainModel: "qwen",
		IndustryKnowledgeBaseIDs: []string{"kb-risk", "kb-law"}, // 覆盖 handler DTO 到 service input 的原样透传。
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/assistant-versions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{Role: domain.UserRolePlatformAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusCreated, resp.Code)
	assert.Equal(t, []string{"kb-risk", "kb-law"}, svc.createInput.IndustryKnowledgeBaseIDs)
}

// TestAVUpdatePassesIndustryKnowledgeBaseIDs 验证编辑请求会透传行业知识库关联 ID 列表。
func TestAVUpdatePassesIndustryKnowledgeBaseIDs(t *testing.T) {
	svc := &avServiceStub{one: service.AssistantVersionResult{ID: "v1", Name: "标准版"}}
	router := newAVTestRouter(t, svc)
	industryIDs := []string{"kb-risk"}
	body, err := json.Marshal(UpdateAssistantVersionRequest{
		Name: "标准版", SystemPrompt: "p", ImageID: "v2026.5.16", MainModel: "qwen",
		IndustryKnowledgeBaseIDs: &industryIDs, // 覆盖 PUT 路径的 DTO 到 service input 透传。
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/assistant-versions/v1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{Role: domain.UserRolePlatformAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, "v1", svc.updateID)
	assert.Equal(t, []string{"kb-risk"}, svc.updateInput.IndustryKnowledgeBaseIDs)
	assert.True(t, svc.updateInput.ReplaceIndustryKnowledgeBases)
}

// TestAVUpdateOmitsIndustryKnowledgeBaseIDs 验证编辑请求省略行业库字段时不会触发替换。
func TestAVUpdateOmitsIndustryKnowledgeBaseIDs(t *testing.T) {
	svc := &avServiceStub{one: service.AssistantVersionResult{ID: "v1", Name: "标准版"}}
	router := newAVTestRouter(t, svc)
	body, err := json.Marshal(UpdateAssistantVersionRequest{
		Name: "标准版", SystemPrompt: "p", ImageID: "v2026.5.16", MainModel: "qwen",
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/assistant-versions/v1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{Role: domain.UserRolePlatformAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)
	assert.False(t, svc.updateInput.ReplaceIndustryKnowledgeBases)
	assert.Empty(t, svc.updateInput.IndustryKnowledgeBaseIDs)
}

// TestAVUpdateClearsIndustryKnowledgeBaseIDs 验证编辑请求显式空数组会触发清空行业库关联。
func TestAVUpdateClearsIndustryKnowledgeBaseIDs(t *testing.T) {
	svc := &avServiceStub{one: service.AssistantVersionResult{ID: "v1", Name: "标准版"}}
	router := newAVTestRouter(t, svc)
	industryIDs := []string{}
	body, err := json.Marshal(UpdateAssistantVersionRequest{
		Name: "标准版", SystemPrompt: "p", ImageID: "v2026.5.16", MainModel: "qwen",
		IndustryKnowledgeBaseIDs: &industryIDs, // 空数组是平台管理员主动清空关联，不等同于字段省略。
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/assistant-versions/v1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{Role: domain.UserRolePlatformAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)
	assert.True(t, svc.updateInput.ReplaceIndustryKnowledgeBases)
	assert.Empty(t, svc.updateInput.IndustryKnowledgeBaseIDs)
}

// TestAVCreateMapsDenied 验证 service 返回 Denied 时映射 403。
func TestAVCreateMapsDenied(t *testing.T) {
	svc := &avServiceStub{err: service.ErrAssistantVersionDenied}
	router := newAVTestRouter(t, svc)
	body, _ := json.Marshal(CreateAssistantVersionRequest{
		Name: "标准版", SystemPrompt: "p", ImageID: "v2026.5.16", MainModel: "qwen",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/assistant-versions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{Role: domain.UserRoleOrgAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusForbidden, resp.Code)
}

// TestAVCreateMapsIndustryKnowledgeNotFound 验证创建版本关联未知行业库时映射 404。
func TestAVCreateMapsIndustryKnowledgeNotFound(t *testing.T) {
	svc := &avServiceStub{err: service.ErrIndustryKnowledgeNotFound}
	router := newAVTestRouter(t, svc)
	body, err := json.Marshal(CreateAssistantVersionRequest{
		Name: "标准版", SystemPrompt: "p", ImageID: "v2026.5.16", MainModel: "qwen",
		IndustryKnowledgeBaseIDs: []string{"missing-industry"}, // 覆盖行业库不存在的 AV 错误映射。
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/assistant-versions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{Role: domain.UserRolePlatformAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusNotFound, resp.Code)
	assert.Contains(t, resp.Body.String(), "INDUSTRY_KNOWLEDGE_NOT_FOUND")
}

// TestAVDeleteMapsInUse 验证 service 返回 InUse 时映射 409。
func TestAVDeleteMapsInUse(t *testing.T) {
	svc := &avServiceStub{err: service.ErrAssistantVersionInUse}
	router := newAVTestRouter(t, svc)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/assistant-versions/v1", nil)
	req = withPrincipal(req, auth.Principal{Role: domain.UserRolePlatformAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusConflict, resp.Code)
}

// TestAVListRuntimeImages 验证镜像列表端点返回配置镜像。
func TestAVListRuntimeImages(t *testing.T) {
	svc := &avServiceStub{images: []service.RuntimeImageOption{{ID: "v2026.5.16", Label: "当前"}}}
	router := newAVTestRouter(t, svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runtime-images", nil)
	req = withPrincipal(req, auth.Principal{Role: domain.UserRolePlatformAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), "v2026.5.16")
}

// avAddSkillRequest 构造一个从库选 skill 的 JSON 请求，供 AddSkillFromLibrary handler 测试复用。
// Name 字段补全，确保 handler 对 req.Name → AddSkillFromLibraryInput.Name 的透传路径也被覆盖。
func avAddSkillRequest(t *testing.T) *http.Request {
	t.Helper()
	body, err := json.Marshal(AddSkillFromLibraryRequest{
		Source: "platform", SourceRef: "weather", Name: "weather", Version: "1.0.0",
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/assistant-versions/v1/skills", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// TestAVAddSkillFromLibraryReturns200 验证从库选 skill 成功时返回 200。
func TestAVAddSkillFromLibraryReturns200(t *testing.T) {
	svc := &avServiceStub{one: service.AssistantVersionResult{ID: "v1", Name: "标准版"}}
	router := newAVTestRouter(t, svc)
	req := withPrincipal(avAddSkillRequest(t), auth.Principal{Role: domain.UserRolePlatformAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)
}

// TestAVAddSkillFromLibraryMapsSkillNameTaken 验证 service 返回 SkillNameTaken 时映射 409。
func TestAVAddSkillFromLibraryMapsSkillNameTaken(t *testing.T) {
	// 同版本内 skill 已存在时，handler 应映射为 409 Conflict。
	svc := &avServiceStub{err: service.ErrAssistantVersionSkillNameTaken}
	router := newAVTestRouter(t, svc)
	req := withPrincipal(avAddSkillRequest(t), auth.Principal{Role: domain.UserRolePlatformAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusConflict, resp.Code)
}

// TestAVAddSkillFromLibraryMapsSkillNotFound 验证 service 返回 PlatformSkillNotFound 时映射 404。
func TestAVAddSkillFromLibraryMapsSkillNotFound(t *testing.T) {
	// 平台库 skill 不存在时，handler 应映射为 404 Not Found。
	svc := &avServiceStub{err: service.ErrPlatformSkillNotFound}
	router := newAVTestRouter(t, svc)
	req := withPrincipal(avAddSkillRequest(t), auth.Principal{Role: domain.UserRolePlatformAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusNotFound, resp.Code)
}

// TestAVAddSkillFromLibraryRejectsMissingBody 验证缺少 JSON 请求体时返回 400。
func TestAVAddSkillFromLibraryRejectsMissingBody(t *testing.T) {
	// 不提交 JSON 请求体，ShouldBindJSON 应失败，handler 返回 400。
	svc := &avServiceStub{}
	router := newAVTestRouter(t, svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/assistant-versions/v1/skills", nil)
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{Role: domain.UserRolePlatformAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusBadRequest, resp.Code)
}

// TestAVAddSkillFromLibraryMapsSourceUnknown 验证 service 返回 ErrAppSkillSourceUnknown 时映射 400。
func TestAVAddSkillFromLibraryMapsSourceUnknown(t *testing.T) {
	// 来源字段传入未知值时（既非 platform 也非 clawhub），service 返回 ErrAppSkillSourceUnknown，
	// handler 应映射为 400 Bad Request 且错误码为 APP_SKILL_SOURCE_UNKNOWN。
	svc := &avServiceStub{err: service.ErrAppSkillSourceUnknown}
	router := newAVTestRouter(t, svc)
	req := withPrincipal(avAddSkillRequest(t), auth.Principal{Role: domain.UserRolePlatformAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusBadRequest, resp.Code)
	assert.Contains(t, resp.Body.String(), "APP_SKILL_SOURCE_UNKNOWN")
}
