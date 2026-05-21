package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
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
	list   []service.AssistantVersionResult
	one    service.AssistantVersionResult
	err    error
	images []service.RuntimeImageOption
}

func (s *avServiceStub) List(context.Context, auth.Principal) ([]service.AssistantVersionResult, error) {
	return s.list, s.err
}
func (s *avServiceStub) Get(context.Context, auth.Principal, string) (service.AssistantVersionResult, error) {
	return s.one, s.err
}
func (s *avServiceStub) Create(context.Context, auth.Principal, service.AssistantVersionInput) (service.AssistantVersionResult, error) {
	return s.one, s.err
}
func (s *avServiceStub) Update(context.Context, auth.Principal, string, service.AssistantVersionInput) (service.AssistantVersionResult, error) {
	return s.one, s.err
}
func (s *avServiceStub) Delete(context.Context, auth.Principal, string) error { return s.err }
func (s *avServiceStub) UploadSkill(context.Context, auth.Principal, string, []byte) (service.AssistantVersionResult, error) {
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

// avUploadRequest 构造一个 multipart skill 上传请求，供 UploadSkill handler 测试复用。
func avUploadRequest(t *testing.T) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	fw, err := w.CreateFormFile("file", "skill.tar")
	require.NoError(t, err)
	_, err = fw.Write([]byte("dummy-tar-bytes"))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/assistant-versions/v1/skills", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

// TestAVUploadSkillReturns200 验证 multipart 上传 skill 成功时返回 200。
func TestAVUploadSkillReturns200(t *testing.T) {
	svc := &avServiceStub{one: service.AssistantVersionResult{ID: "v1", Name: "标准版"}}
	router := newAVTestRouter(t, svc)
	req := withPrincipal(avUploadRequest(t), auth.Principal{Role: domain.UserRolePlatformAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)
}

// TestAVUploadSkillMapsTooLarge 验证 service 返回 SkillTooLarge 时映射 413。
func TestAVUploadSkillMapsTooLarge(t *testing.T) {
	svc := &avServiceStub{err: service.ErrSkillTooLarge}
	router := newAVTestRouter(t, svc)
	req := withPrincipal(avUploadRequest(t), auth.Principal{Role: domain.UserRolePlatformAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.Code)
}

// TestAVUploadSkillRejectsMissingFile 验证缺少 file 表单字段时返回 400。
func TestAVUploadSkillRejectsMissingFile(t *testing.T) {
	svc := &avServiceStub{}
	router := newAVTestRouter(t, svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/assistant-versions/v1/skills", nil)
	req = withPrincipal(req, auth.Principal{Role: domain.UserRolePlatformAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusBadRequest, resp.Code)
}
