package handlers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// knowledgeServiceStub 实现 knowledgeService 接口，仅 stub 测试用到的方法。
type knowledgeServiceStub struct {
	listOrgResult   service.KnowledgeListResult
	listOrgErr      error
	saveOrgResult   service.KnowledgeDocumentResult
	saveOrgErr      error
	saveOrgCalls    int
	saveOrgBodyType string
	saveAppCalls    int
	reparseOrgErr   error
	openContent     string
	openSize        int64
	openName        string
	openErr         error
	openCloses      int
	clearOrgErr     error
	clearOrgID      string

	listEmbeddingModelsResult service.KnowledgeEmbeddingModelListResult
	listEmbeddingModelsErr    error

	ragflowDatasetResult   service.KnowledgeRAGFlowDatasetInfoResult
	ragflowDatasetErr      error
	ragflowDatasetScope    string
	ragflowDatasetTargetID string

	updateEmbeddingModelResult   service.KnowledgeRAGFlowDatasetInfoResult
	updateEmbeddingModelErr      error
	updateEmbeddingModelScope    string
	updateEmbeddingModelTargetID string
	updateEmbeddingModelInput    service.KnowledgeEmbeddingModelInput
}

func (s *knowledgeServiceStub) ListOrg(_ context.Context, _ auth.Principal, _ string, _, _ int32, _, _ string) (service.KnowledgeListResult, error) {
	return s.listOrgResult, s.listOrgErr
}

func (s *knowledgeServiceStub) SaveOrgFile(_ context.Context, _ auth.Principal, _, _ string, content io.Reader, _ int64) (service.KnowledgeDocumentResult, error) {
	s.saveOrgCalls++
	s.saveOrgBodyType = fmt.Sprintf("%T", content)
	return s.saveOrgResult, s.saveOrgErr
}

func (s *knowledgeServiceStub) OpenOrgFile(_ context.Context, _ auth.Principal, _, _ string) (io.ReadCloser, int64, string, error) {
	if s.openErr != nil {
		return nil, 0, "", s.openErr
	}
	return &trackedReadCloser{Buffer: bytes.NewBufferString(s.openContent), onClose: func() { s.openCloses++ }}, s.openSize, s.openName, nil
}

func (s *knowledgeServiceStub) DeleteOrgFile(_ context.Context, _ auth.Principal, _, _ string) error {
	return nil
}

func (s *knowledgeServiceStub) ClearOrgFiles(_ context.Context, _ auth.Principal, orgID string) error {
	s.clearOrgID = orgID
	return s.clearOrgErr
}

func (s *knowledgeServiceStub) ReparseOrgFile(_ context.Context, _ auth.Principal, _, _ string) (service.KnowledgeDocumentResult, error) {
	return service.KnowledgeDocumentResult{}, s.reparseOrgErr
}

func (s *knowledgeServiceStub) ListApp(context.Context, auth.Principal, string, int32, int32, string, string) (service.KnowledgeListResult, error) {
	return service.KnowledgeListResult{}, nil
}

func (s *knowledgeServiceStub) SaveAppFile(context.Context, auth.Principal, string, string, io.Reader, int64) (service.KnowledgeDocumentResult, error) {
	s.saveAppCalls++
	return service.KnowledgeDocumentResult{}, nil
}

func (s *knowledgeServiceStub) OpenAppFile(context.Context, auth.Principal, string, string) (io.ReadCloser, int64, string, error) {
	return io.NopCloser(bytes.NewBuffer(nil)), 0, "file.md", nil
}

func (s *knowledgeServiceStub) DeleteAppFile(context.Context, auth.Principal, string, string) error {
	return nil
}

func (s *knowledgeServiceStub) ReparseAppFile(context.Context, auth.Principal, string, string) (service.KnowledgeDocumentResult, error) {
	return service.KnowledgeDocumentResult{}, nil
}

// 分片上传方法：当前路由测试不覆盖具体行为，stub 返回零值满足接口即可。
func (s *knowledgeServiceStub) InitOrgUpload(context.Context, auth.Principal, string, string, int64) (service.KnowledgeUploadInitResult, error) {
	return service.KnowledgeUploadInitResult{}, nil
}

func (s *knowledgeServiceStub) UploadOrgPart(context.Context, auth.Principal, string, string, int32, io.Reader, int64) error {
	return nil
}

func (s *knowledgeServiceStub) CompleteOrgUpload(context.Context, auth.Principal, string, string) (service.KnowledgeDocumentResult, error) {
	return service.KnowledgeDocumentResult{}, nil
}

func (s *knowledgeServiceStub) AbortOrgUpload(context.Context, auth.Principal, string, string) error {
	return nil
}

func (s *knowledgeServiceStub) InitAppUpload(context.Context, auth.Principal, string, string, int64) (service.KnowledgeUploadInitResult, error) {
	return service.KnowledgeUploadInitResult{}, nil
}

func (s *knowledgeServiceStub) UploadAppPart(context.Context, auth.Principal, string, string, int32, io.Reader, int64) error {
	return nil
}

func (s *knowledgeServiceStub) CompleteAppUpload(context.Context, auth.Principal, string, string) (service.KnowledgeDocumentResult, error) {
	return service.KnowledgeDocumentResult{}, nil
}

func (s *knowledgeServiceStub) AbortAppUpload(context.Context, auth.Principal, string, string) error {
	return nil
}

func (s *knowledgeServiceStub) ListKnowledgeEmbeddingModels(context.Context, auth.Principal) (service.KnowledgeEmbeddingModelListResult, error) {
	return s.listEmbeddingModelsResult, s.listEmbeddingModelsErr
}

func (s *knowledgeServiceStub) GetKnowledgeRAGFlowDatasetInfo(_ context.Context, _ auth.Principal, scope, targetID string) (service.KnowledgeRAGFlowDatasetInfoResult, error) {
	s.ragflowDatasetScope = scope
	s.ragflowDatasetTargetID = targetID
	return s.ragflowDatasetResult, s.ragflowDatasetErr
}

func (s *knowledgeServiceStub) UpdateKnowledgeEmbeddingModel(_ context.Context, _ auth.Principal, scope, targetID string, input service.KnowledgeEmbeddingModelInput) (service.KnowledgeRAGFlowDatasetInfoResult, error) {
	s.updateEmbeddingModelScope = scope
	s.updateEmbeddingModelTargetID = targetID
	s.updateEmbeddingModelInput = input
	return s.updateEmbeddingModelResult, s.updateEmbeddingModelErr
}

// trackedReadCloser 用于测试下载流在响应写出后是否被负责写响应的代码关闭。
type trackedReadCloser struct {
	*bytes.Buffer
	onClose func()
}

func (r *trackedReadCloser) Close() error {
	if r.onClose != nil {
		r.onClose()
	}
	return nil
}

func newKnowledgeTestRouter(t *testing.T, svc knowledgeService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterKnowledgeRoutes(router, NewKnowledgeHandler(svc))
	return router
}

func newKnowledgeTestRouterWithTransferLimit(t *testing.T, svc knowledgeService, limit TransferLimitConfig) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterKnowledgeRoutes(router, NewKnowledgeHandler(svc, limit))
	return router
}

// TestKnowledgeListOrgFlatContract 验证知识库列表返回扁平 items/total 契约，不再返回旧文件树字段。
func TestKnowledgeListOrgFlatContract(t *testing.T) {
	stub := &knowledgeServiceStub{listOrgResult: service.KnowledgeListResult{
		Items: []service.KnowledgeDocumentResult{{ID: "doc-1", Name: "report.md", ParseStatus: "completed"}},
		Total: 1,
	}}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/knowledge", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"items"`)
	assert.Contains(t, w.Body.String(), `"total"`)
	assert.NotContains(t, w.Body.String(), `"path"`)
	assert.NotContains(t, w.Body.String(), `"entries"`)
}

// TestKnowledgeUploadOrgReturnsDocument 验证上传文件后返回 202 和 queued document 视图。
func TestKnowledgeUploadOrgReturnsDocument(t *testing.T) {
	stub := &knowledgeServiceStub{saveOrgResult: service.KnowledgeDocumentResult{ID: "doc-1", Name: "report.md", ParseStatus: "queued"}}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/knowledge?filename=report.md", bytes.NewBufferString("content"))
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Contains(t, w.Body.String(), `"id":"doc-1"`)
	assert.Contains(t, w.Body.String(), `"parse_status":"queued"`)
}

// TestKnowledgeUploadOrgAppliesUploadRateLimit 验证企业知识库上传在进入 service 前会按配置包装请求体。
func TestKnowledgeUploadOrgAppliesUploadRateLimit(t *testing.T) {
	stub := &knowledgeServiceStub{saveOrgResult: service.KnowledgeDocumentResult{ID: "doc-1", Name: "report.md"}}
	router := newKnowledgeTestRouterWithTransferLimit(t, stub, TransferLimitConfig{UploadBytesPerSec: 1 << 20})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/knowledge?filename=report.md", bytes.NewBufferString("content"))
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, 1, stub.saveOrgCalls)
	assert.Contains(t, stub.saveOrgBodyType, "rateLimitedReadCloser")
}

// TestKnowledgeUploadOrgRequiresFilename 验证上传缺少 filename 时在 handler 层直接返回 400。
func TestKnowledgeUploadOrgRequiresFilename(t *testing.T) {
	stub := &knowledgeServiceStub{}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/knowledge", bytes.NewBufferString("content"))
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestKnowledgeUploadOrgRejectsOversizedBody 验证后端在调用 service 前拒绝超过上限的企业知识库上传。
func TestKnowledgeUploadOrgRejectsOversizedBody(t *testing.T) {
	stub := &knowledgeServiceStub{}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/knowledge?filename=big.md", bytes.NewBufferString("x"))
	req.Header.Set("Content-Length", strconv.FormatInt(maxKnowledgeUploadBytes+1, 10))
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), maxKnowledgeUploadMessage)
	assert.Equal(t, 0, stub.saveOrgCalls)
}

// TestKnowledgeUploadOrgRejectsUnknownContentLength 验证未知请求体大小时不允许上传，避免 RAGFlow 上传后才发现超限。
func TestKnowledgeUploadOrgRejectsUnknownContentLength(t *testing.T) {
	stub := &knowledgeServiceStub{}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/knowledge?filename=stream.md", io.NopCloser(bytes.NewBufferString("content")))
	req.ContentLength = -1
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, 0, stub.saveOrgCalls)
}

// TestKnowledgeUploadOrgMapsQuotaExceeded 验证知识库空间不足映射为 409。
func TestKnowledgeUploadOrgMapsQuotaExceeded(t *testing.T) {
	stub := &knowledgeServiceStub{saveOrgErr: fmt.Errorf("%w: 知识库空间不足，剩余 1MB", service.ErrKnowledgeQuotaExceeded)}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/knowledge?filename=big.md", bytes.NewBufferString("content"))
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "KNOWLEDGE_QUOTA_EXCEEDED")
	assert.Contains(t, w.Body.String(), "剩余 1MB")
}

// TestKnowledgeClearOrgFilesRoute 验证集合级 DELETE 会清空企业知识库文件内容，并把企业 ID 传给 service。
func TestKnowledgeClearOrgFilesRoute(t *testing.T) {
	stub := &knowledgeServiceStub{}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/organizations/org-1/knowledge", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "org-1", stub.clearOrgID)
}

// TestKnowledgeClearOrgFilesMapsForbidden 验证清空企业知识库文件的权限错误仍按知识库统一错误码返回。
func TestKnowledgeClearOrgFilesMapsForbidden(t *testing.T) {
	stub := &knowledgeServiceStub{clearOrgErr: service.ErrKnowledgeForbidden}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/organizations/org-1/knowledge", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u2", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "KNOWLEDGE_FORBIDDEN")
}

// TestKnowledgeReparseOrgRequiresDocumentID 验证重解析必须通过路由携带 documentId，未知 document 映射为 404。
func TestKnowledgeReparseOrgRequiresDocumentID(t *testing.T) {
	stub := &knowledgeServiceStub{reparseOrgErr: service.ErrNotFound}
	router := newKnowledgeTestRouter(t, stub)

	missingRoute := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/knowledge//reparse", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(missingRoute, req)
	assert.Equal(t, http.StatusNotFound, missingRoute.Code)

	unknownDoc := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/knowledge/doc-404/reparse", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(unknownDoc, req)
	assert.Equal(t, http.StatusNotFound, unknownDoc.Code)
}

// TestKnowledgeSyncRoutesRemoved 验证旧同步状态路由已从知识库管理面移除。
func TestKnowledgeSyncRoutesRemoved(t *testing.T) {
	router := newKnowledgeTestRouter(t, &knowledgeServiceStub{})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/knowledge/sync-status", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestKnowledgeDownloadOrgUsesServiceFilename 验证下载文件名来自 service 返回的 document 元数据，而不是用户输入。
func TestKnowledgeDownloadOrgUsesServiceFilename(t *testing.T) {
	stub := &knowledgeServiceStub{openContent: "hello", openSize: 5, openName: "report.md"}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/knowledge/doc-1/file", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "attachment; filename=report.md", w.Header().Get("Content-Disposition"))
	assert.Equal(t, "hello", w.Body.String())
	assert.Equal(t, 1, stub.openCloses)
}

// TestKnowledgeDownloadOrgKeepsResponseHeadersWithRateLimit 验证下载限速不改变文件名、长度和响应体契约。
func TestKnowledgeDownloadOrgKeepsResponseHeadersWithRateLimit(t *testing.T) {
	stub := &knowledgeServiceStub{openContent: "hello", openSize: 5, openName: "report.md"}
	router := newKnowledgeTestRouterWithTransferLimit(t, stub, TransferLimitConfig{DownloadBytesPerSec: 1 << 20})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/knowledge/doc-1/file", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "attachment; filename=report.md", w.Header().Get("Content-Disposition"))
	assert.Equal(t, "5", w.Header().Get("Content-Length"))
	assert.Equal(t, "hello", w.Body.String())
	assert.Equal(t, 1, stub.openCloses)
}

// TestKnowledgeListEmbeddingModelsRoutesToService 验证平台可选 embedding 模型列表路由透传 service，并保持 items 响应契约。
func TestKnowledgeListEmbeddingModelsRoutesToService(t *testing.T) {
	stub := &knowledgeServiceStub{listEmbeddingModelsResult: service.KnowledgeEmbeddingModelListResult{
		Items: []service.KnowledgeEmbeddingModelResult{{Name: "BAAI/bge-m3", Label: "BGE M3", Provider: "OpenAI-API-Compatible", Available: true}},
	}}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/knowledge/embedding-models", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"items"`)
	assert.Contains(t, w.Body.String(), `"name":"BAAI/bge-m3"`)
	assert.Contains(t, w.Body.String(), `"provider":"OpenAI-API-Compatible"`)
}

// TestKnowledgeGetOrgRAGFlowDatasetRoutesToService 验证企业 RAGFlow dataset 查询路由使用 org scope 和企业 ID 调用 service。
func TestKnowledgeGetOrgRAGFlowDatasetRoutesToService(t *testing.T) {
	stub := &knowledgeServiceStub{ragflowDatasetResult: service.KnowledgeRAGFlowDatasetInfoResult{
		Scope: service.KnowledgeRAGFlowScopeOrg, TargetID: "org-1", TargetName: "企业一", Status: "ok",
	}}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/knowledge/ragflow-dataset", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, service.KnowledgeRAGFlowScopeOrg, stub.ragflowDatasetScope)
	assert.Equal(t, "org-1", stub.ragflowDatasetTargetID)
	assert.Contains(t, w.Body.String(), `"status":"ok"`)
}

// TestKnowledgePatchOrgEmbeddingModelBindsHumanModelName 验证企业模型修改只接收人类可读模型名和 provider，并返回异步处理状态。
func TestKnowledgePatchOrgEmbeddingModelBindsHumanModelName(t *testing.T) {
	stub := &knowledgeServiceStub{updateEmbeddingModelResult: service.KnowledgeRAGFlowDatasetInfoResult{
		Scope: service.KnowledgeRAGFlowScopeOrg, TargetID: "org-1", TargetName: "企业一", Status: "ok",
	}}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/organizations/org-1/knowledge/ragflow-dataset/embedding-model", bytes.NewBufferString(`{"name":"BAAI/bge-m3","provider":"OpenAI-API-Compatible"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, service.KnowledgeRAGFlowScopeOrg, stub.updateEmbeddingModelScope)
	assert.Equal(t, "org-1", stub.updateEmbeddingModelTargetID)
	assert.Equal(t, "BAAI/bge-m3", stub.updateEmbeddingModelInput.Name)
	assert.Equal(t, "OpenAI-API-Compatible", stub.updateEmbeddingModelInput.Provider)
}

// TestKnowledgeGetAppRAGFlowDatasetRoutesToService 验证应用 RAGFlow dataset 查询路由使用 app scope 和实例 ID 调用 service。
func TestKnowledgeGetAppRAGFlowDatasetRoutesToService(t *testing.T) {
	stub := &knowledgeServiceStub{ragflowDatasetResult: service.KnowledgeRAGFlowDatasetInfoResult{
		Scope: service.KnowledgeRAGFlowScopeApp, TargetID: "app-1", TargetName: "实例一", Status: "ok",
	}}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/knowledge/ragflow-dataset", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, service.KnowledgeRAGFlowScopeApp, stub.ragflowDatasetScope)
	assert.Equal(t, "app-1", stub.ragflowDatasetTargetID)
	assert.Contains(t, w.Body.String(), `"status":"ok"`)
}

// TestKnowledgePatchAppEmbeddingModelRejectsMissingName 验证应用模型修改缺少模型名称时在 handler 层返回统一业务文案。
func TestKnowledgePatchAppEmbeddingModelRejectsMissingName(t *testing.T) {
	stub := &knowledgeServiceStub{}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/apps/app-1/knowledge/ragflow-dataset/embedding-model", bytes.NewBufferString(`{"provider":"OpenAI-API-Compatible"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"BAD_REQUEST"`)
	assert.Contains(t, w.Body.String(), "模型名称不能为空")
	assert.Empty(t, stub.updateEmbeddingModelTargetID)
}

