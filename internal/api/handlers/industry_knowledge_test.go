package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// industryKnowledgeServiceStub 实现行业知识库 handler 依赖的 service 接口，仅记录断言需要的调用参数。
type industryKnowledgeServiceStub struct {
	listResult service.IndustryKnowledgeBaseListResult
	listErr    error

	createResult service.IndustryKnowledgeBaseResult
	createErr    error
	createName   string

	renameResult service.IndustryKnowledgeBaseResult
	renameErr    error
	renameID     string
	renameName   string

	deleteErr error
	deleteID  string

	listFilesResult        service.KnowledgeListResult
	listFilesErr           error
	listFilesIndustryID    string
	listFilesPage          int32
	listFilesPageSize      int32
	listFilesKeyword       string
	listFilesStatus        string
	listFilesCreatedFrom   time.Time
	listFilesCreatedBefore time.Time

	saveResult     service.KnowledgeDocumentResult
	saveErr        error
	saveCalls      int
	saveIndustryID string
	saveFilename   string
	saveSize       int64
	saveBodyType   string
	saveContent    string

	openContent string
	openSize    int64
	openName    string
	openErr     error

	deleteFileErr        error
	deleteFileIndustryID string
	deleteFileDocumentID string

	clearFilesErr        error
	clearFilesIndustryID string

	reparseResult     service.KnowledgeDocumentResult
	reparseErr        error
	reparseIndustryID string
	reparseDocumentID string

	ragflowDatasetResult   service.KnowledgeRAGFlowDatasetInfoResult
	ragflowDatasetErr      error
	ragflowDatasetScope    string
	ragflowDatasetTargetID string

	updateEmbeddingModelResult   service.KnowledgeRAGFlowDatasetInfoResult
	updateEmbeddingModelErr      error
	updateEmbeddingModelScope    string
	updateEmbeddingModelTargetID string
	updateEmbeddingModelInput    service.KnowledgeEmbeddingModelInput

	externalUploadCalls  int
	externalIndustryName string
	externalFilename     string
	externalSize         int64
	externalContent      string
	externalUploadErr    error
}

func (s *industryKnowledgeServiceStub) ListIndustryKnowledgeBases(_ context.Context, _ auth.Principal, _, _ int32, _ string) (service.IndustryKnowledgeBaseListResult, error) {
	return s.listResult, s.listErr
}

func (s *industryKnowledgeServiceStub) CreateIndustryKnowledgeBase(_ context.Context, _ auth.Principal, name string) (service.IndustryKnowledgeBaseResult, error) {
	s.createName = name
	return s.createResult, s.createErr
}

func (s *industryKnowledgeServiceStub) RenameIndustryKnowledgeBase(_ context.Context, _ auth.Principal, id, name string) (service.IndustryKnowledgeBaseResult, error) {
	s.renameID = id
	s.renameName = name
	return s.renameResult, s.renameErr
}

func (s *industryKnowledgeServiceStub) DeleteIndustryKnowledgeBase(_ context.Context, _ auth.Principal, id string) error {
	s.deleteID = id
	return s.deleteErr
}

func (s *industryKnowledgeServiceStub) ListIndustryFiles(_ context.Context, _ auth.Principal, industryID string, page, pageSize int32, keyword, status string, createdFrom, createdBefore time.Time) (service.KnowledgeListResult, error) {
	s.listFilesIndustryID = industryID
	s.listFilesPage = page
	s.listFilesPageSize = pageSize
	s.listFilesKeyword = keyword
	s.listFilesStatus = status
	s.listFilesCreatedFrom = createdFrom
	s.listFilesCreatedBefore = createdBefore
	return s.listFilesResult, s.listFilesErr
}

func (s *industryKnowledgeServiceStub) SaveIndustryFile(_ context.Context, _ auth.Principal, industryID, filename string, content io.Reader, size int64) (service.KnowledgeDocumentResult, error) {
	s.saveCalls++
	s.saveIndustryID = industryID
	s.saveFilename = filename
	s.saveSize = size
	s.saveBodyType = fmt.Sprintf("%T", content)
	body, _ := io.ReadAll(content)
	s.saveContent = string(body)
	return s.saveResult, s.saveErr
}

func (s *industryKnowledgeServiceStub) OpenIndustryFile(_ context.Context, _ auth.Principal, _ string, _ string) (io.ReadCloser, int64, string, error) {
	if s.openErr != nil {
		return nil, 0, "", s.openErr
	}
	return io.NopCloser(bytes.NewBufferString(s.openContent)), s.openSize, s.openName, nil
}

func (s *industryKnowledgeServiceStub) DeleteIndustryFile(_ context.Context, _ auth.Principal, industryID, documentID string) error {
	s.deleteFileIndustryID = industryID
	s.deleteFileDocumentID = documentID
	return s.deleteFileErr
}

func (s *industryKnowledgeServiceStub) ClearIndustryFiles(_ context.Context, _ auth.Principal, industryID string) error {
	s.clearFilesIndustryID = industryID
	return s.clearFilesErr
}

func (s *industryKnowledgeServiceStub) ReparseIndustryFile(_ context.Context, _ auth.Principal, industryID, documentID string) (service.KnowledgeDocumentResult, error) {
	s.reparseIndustryID = industryID
	s.reparseDocumentID = documentID
	return s.reparseResult, s.reparseErr
}

func (s *industryKnowledgeServiceStub) GetKnowledgeRAGFlowDatasetInfo(_ context.Context, _ auth.Principal, scope, targetID string) (service.KnowledgeRAGFlowDatasetInfoResult, error) {
	s.ragflowDatasetScope = scope
	s.ragflowDatasetTargetID = targetID
	return s.ragflowDatasetResult, s.ragflowDatasetErr
}

func (s *industryKnowledgeServiceStub) UpdateKnowledgeEmbeddingModel(_ context.Context, _ auth.Principal, scope, targetID string, input service.KnowledgeEmbeddingModelInput) (service.KnowledgeRAGFlowDatasetInfoResult, error) {
	s.updateEmbeddingModelScope = scope
	s.updateEmbeddingModelTargetID = targetID
	s.updateEmbeddingModelInput = input
	return s.updateEmbeddingModelResult, s.updateEmbeddingModelErr
}

func (s *industryKnowledgeServiceStub) ExternalUploadIndustryFile(_ context.Context, industryName, filename string, content io.Reader, size int64) (service.KnowledgeDocumentResult, error) {
	s.externalUploadCalls++
	s.externalIndustryName = industryName
	s.externalFilename = filename
	s.externalSize = size
	body, _ := io.ReadAll(content)
	s.externalContent = string(body)
	return s.saveResult, s.externalUploadErr
}

// newIndustryKnowledgeTestRouter 构造同时挂载外部上传和平台管理路由的 Gin 测试引擎。
func newIndustryKnowledgeTestRouter(t *testing.T, svc industryKnowledgeService, uploadToken string, limits ...TransferLimitConfig) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	handler := NewIndustryKnowledgeHandler(svc, uploadToken, limits...)
	RegisterExternalIndustryKnowledgeRoutes(router, handler)
	RegisterIndustryKnowledgeRoutes(router, handler)
	return router
}

// TestIndustryKnowledgeUploadTokenReturnsConfiguredValue 验证平台管理员可读取配置中的外部上传 token，供前端接口文档直接展示真实调用值。
func TestIndustryKnowledgeUploadTokenReturnsConfiguredValue(t *testing.T) {
	stub := &industryKnowledgeServiceStub{}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/industry-knowledge-bases/upload-token", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "secret-token", body["upload_token"])
}

// TestIndustryKnowledgeUploadTokenRejectsOrgAdmin 验证外部上传 token 只暴露给平台管理员，避免企业侧用户拿到平台级同步凭据。
func TestIndustryKnowledgeUploadTokenRejectsOrgAdmin(t *testing.T) {
	stub := &industryKnowledgeServiceStub{}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/industry-knowledge-bases/upload-token", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u-org-admin", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.NotContains(t, w.Body.String(), "secret-token")
}

// TestIndustryKnowledgeListFilesPassesSearchPaginationAndCreatedDateRange 验证行业库文件列表把文件名、分页和创建日期条件传给 service；
// created_to 按用户选择的自然日闭区间处理，转成下一日零点前的开区间上界，避免漏掉当天文件。
func TestIndustryKnowledgeListFilesPassesSearchPaginationAndCreatedDateRange(t *testing.T) {
	stub := &industryKnowledgeServiceStub{}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/industry-knowledge-bases/industry-1/knowledge?page=2&page_size=20&keyword=policy&status=completed&created_from=2026-06-01&created_to=2026-06-05", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "industry-1", stub.listFilesIndustryID)
	assert.Equal(t, int32(2), stub.listFilesPage)
	assert.Equal(t, int32(20), stub.listFilesPageSize)
	assert.Equal(t, "policy", stub.listFilesKeyword)
	assert.Equal(t, "completed", stub.listFilesStatus)
	assert.Equal(t, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), stub.listFilesCreatedFrom)
	assert.Equal(t, time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC), stub.listFilesCreatedBefore)
}

// TestIndustryKnowledgeListFilesRejectsInvalidCreatedDate 验证创建日期筛选只接受 YYYY-MM-DD，
// 防止模糊时间字符串进入 SQL 层造成跨时区或解析差异。
func TestIndustryKnowledgeListFilesRejectsInvalidCreatedDate(t *testing.T) {
	stub := &industryKnowledgeServiceStub{}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/industry-knowledge-bases/industry-1/knowledge?created_from=2026-06", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Empty(t, stub.listFilesIndustryID)
}

// TestIndustryKnowledgeClearFilesRoute 验证集合级 DELETE 只清空指定行业库文件内容，不删除行业库记录。
func TestIndustryKnowledgeClearFilesRoute(t *testing.T) {
	stub := &industryKnowledgeServiceStub{}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/industry-knowledge-bases/industry-1/knowledge", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "industry-1", stub.clearFilesIndustryID)
	assert.Empty(t, stub.deleteID)
}

// TestIndustryKnowledgeClearFilesMapsMissingBase 验证清空不存在的行业库文件内容时返回行业库专属 404。
func TestIndustryKnowledgeClearFilesMapsMissingBase(t *testing.T) {
	stub := &industryKnowledgeServiceStub{clearFilesErr: service.ErrIndustryKnowledgeNotFound}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/industry-knowledge-bases/missing/knowledge", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "INDUSTRY_KNOWLEDGE_NOT_FOUND")
}

// multipartIndustryUploadBody 构造外部上传接口需要的 multipart/form-data 请求体。
func multipartIndustryUploadBody(t *testing.T, industryName, filename, content string) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	require.NoError(t, writer.WriteField("industry_name", industryName))
	fileWriter, err := writer.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = fileWriter.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return body, writer.FormDataContentType()
}

// multipartIndustryNameOnlyBody 构造只有行业名称、缺少 file 字段的 multipart/form-data 请求体。
func multipartIndustryNameOnlyBody(t *testing.T, industryName string) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	require.NoError(t, writer.WriteField("industry_name", industryName))
	require.NoError(t, writer.Close())
	return body, writer.FormDataContentType()
}

// TestExternalIndustryUploadRejectsMissingToken 验证外部上传缺少固定鉴权字符串时返回 401 且不调用 service。
func TestExternalIndustryUploadRejectsMissingToken(t *testing.T) {
	stub := &industryKnowledgeServiceStub{}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token")

	body, contentType := multipartIndustryUploadBody(t, "保险", "policy.pdf", "content")
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/external/industry-knowledge/files", body)
	req.Header.Set("Content-Type", contentType)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, 0, stub.externalUploadCalls)
}

// TestExternalIndustryUploadAcceptsConfiguredToken 验证外部上传携带配置 token 时透传 industry_name 和文件给 service。
func TestExternalIndustryUploadAcceptsConfiguredToken(t *testing.T) {
	stub := &industryKnowledgeServiceStub{saveResult: service.KnowledgeDocumentResult{ID: "doc-1", Name: "policy.pdf", ParseStatus: "queued"}}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token")

	body, contentType := multipartIndustryUploadBody(t, "保险", "policy.pdf", "content")
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/external/industry-knowledge/files", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-OC-Industry-Knowledge-Token", "secret-token")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, "保险", stub.externalIndustryName)
	assert.Equal(t, "policy.pdf", stub.externalFilename)
	assert.Equal(t, "content", stub.externalContent)
}

// TestExternalIndustryUploadParsesMultipartWithUploadRateLimit 验证外部行业库 multipart 上传在限速开启时仍能正常解析文件。
func TestExternalIndustryUploadParsesMultipartWithUploadRateLimit(t *testing.T) {
	stub := &industryKnowledgeServiceStub{saveResult: service.KnowledgeDocumentResult{ID: "doc-1", Name: "policy.pdf"}}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token", TransferLimitConfig{UploadBytesPerSec: 1 << 20})

	body, contentType := multipartIndustryUploadBody(t, "保险", "policy.pdf", "content")
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/external/industry-knowledge/files", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-OC-Industry-Knowledge-Token", "secret-token")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, 1, stub.externalUploadCalls)
	assert.Equal(t, "保险", stub.externalIndustryName)
	assert.Equal(t, "policy.pdf", stub.externalFilename)
	assert.Equal(t, "content", stub.externalContent)
}

// TestExternalIndustryUploadRejectsEmptyConfiguredToken 验证未配置固定鉴权字符串时外部入口保持禁用。
func TestExternalIndustryUploadRejectsEmptyConfiguredToken(t *testing.T) {
	stub := &industryKnowledgeServiceStub{}
	router := newIndustryKnowledgeTestRouter(t, stub, "")

	body, contentType := multipartIndustryUploadBody(t, "保险", "policy.pdf", "content")
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/external/industry-knowledge/files", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-OC-Industry-Knowledge-Token", "")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, 0, stub.externalUploadCalls)
}

// TestExternalIndustryUploadRejectsOversizedContentLength 验证合法 token 请求声明体积超限时在解析 multipart 前拒绝。
func TestExternalIndustryUploadRejectsOversizedContentLength(t *testing.T) {
	stub := &industryKnowledgeServiceStub{}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token")

	body, contentType := multipartIndustryUploadBody(t, "保险", "policy.pdf", "content")
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/external/industry-knowledge/files", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Content-Length", strconv.FormatInt(maxKnowledgeUploadBytes+maxKnowledgeMultipartOverheadBytes+1, 10))
	req.Header.Set("X-OC-Industry-Knowledge-Token", "secret-token")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), maxKnowledgeUploadMessage)
	assert.Equal(t, 0, stub.externalUploadCalls)
}

// TestExternalIndustryUploadRejectsBlankIndustryName 验证空白行业名称会在 handler 层拒绝，避免无意义请求进入 service。
func TestExternalIndustryUploadRejectsBlankIndustryName(t *testing.T) {
	stub := &industryKnowledgeServiceStub{}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token")

	body, contentType := multipartIndustryUploadBody(t, "  ", "policy.pdf", "content")
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/external/industry-knowledge/files", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-OC-Industry-Knowledge-Token", "secret-token")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, 0, stub.externalUploadCalls)
}

// TestExternalIndustryUploadRejectsMissingFile 验证外部上传缺少 file 字段时不调用 service。
func TestExternalIndustryUploadRejectsMissingFile(t *testing.T) {
	stub := &industryKnowledgeServiceStub{}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token")

	body, contentType := multipartIndustryNameOnlyBody(t, "保险")
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/external/industry-knowledge/files", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-OC-Industry-Knowledge-Token", "secret-token")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, 0, stub.externalUploadCalls)
}

// TestIndustryKnowledgePlatformRoutesRequirePlatformAdmin 验证平台行业库管理接口拒绝非平台管理员。
func TestIndustryKnowledgePlatformRoutesRequirePlatformAdmin(t *testing.T) {
	stub := &industryKnowledgeServiceStub{listErr: service.ErrKnowledgeForbidden}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/industry-knowledge-bases", nil)
	req = withPrincipal(req, auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestIndustryKnowledgeCreateMapsNameTaken 验证创建行业库的重名错误映射为 409，且请求体名称透传给 service。
func TestIndustryKnowledgeCreateMapsNameTaken(t *testing.T) {
	stub := &industryKnowledgeServiceStub{createErr: service.ErrIndustryKnowledgeNameTaken}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/industry-knowledge-bases", bytes.NewBufferString(`{"name":"保险"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "保险", stub.createName)
	assert.Contains(t, w.Body.String(), "INDUSTRY_KNOWLEDGE_NAME_TAKEN")
}

// TestIndustryKnowledgeCreateRejectsBlankName 验证创建行业库时空白名称在 handler 层直接拒绝。
func TestIndustryKnowledgeCreateRejectsBlankName(t *testing.T) {
	stub := &industryKnowledgeServiceStub{}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/industry-knowledge-bases", bytes.NewBufferString(`{"name":"  "}`))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Empty(t, stub.createName)
}

// TestIndustryKnowledgeGenericErrorMapsInternal 验证非哨兵运行时错误不会被映射为客户端 400，也不会泄漏底层敏感细节。
func TestIndustryKnowledgeGenericErrorMapsInternal(t *testing.T) {
	stub := &industryKnowledgeServiceStub{listErr: errors.New("database password=secret-token query failed")}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/industry-knowledge-bases", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "INTERNAL")
	assert.NotContains(t, w.Body.String(), "secret-token")
	assert.NotContains(t, w.Body.String(), "database")
}

// TestIndustryKnowledgeUploadUsesOctetStream 验证平台侧上传沿用 filename query 和 octet-stream 文件流契约。
func TestIndustryKnowledgeUploadUsesOctetStream(t *testing.T) {
	stub := &industryKnowledgeServiceStub{saveResult: service.KnowledgeDocumentResult{ID: "doc-1", Name: "policy.pdf", ParseStatus: "queued"}}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/industry-knowledge-bases/industry-1/knowledge?filename=policy.pdf", bytes.NewBufferString("content"))
	req.Header.Set("Content-Type", "application/octet-stream")
	req = withPrincipal(req, auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, 1, stub.saveCalls)
	assert.Equal(t, "industry-1", stub.saveIndustryID)
	assert.Equal(t, "policy.pdf", stub.saveFilename)
	assert.Equal(t, int64(len("content")), stub.saveSize)
	assert.Equal(t, "content", stub.saveContent)
}

// TestIndustryKnowledgeUploadAppliesUploadRateLimit 验证平台行业库上传在进入 service 前使用上传限速 reader。
func TestIndustryKnowledgeUploadAppliesUploadRateLimit(t *testing.T) {
	stub := &industryKnowledgeServiceStub{saveResult: service.KnowledgeDocumentResult{ID: "doc-1", Name: "policy.pdf"}}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token", TransferLimitConfig{UploadBytesPerSec: 1 << 20})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/industry-knowledge-bases/industry-1/knowledge?filename=policy.pdf", bytes.NewBufferString("content"))
	req.Header.Set("Content-Type", "application/octet-stream")
	req = withPrincipal(req, auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, 1, stub.saveCalls)
	assert.Contains(t, stub.saveBodyType, "rateLimitedReadCloser")
}

// TestIndustryKnowledgeGetRAGFlowDatasetRoutesToService 验证行业库 RAGFlow dataset 查询路由使用 industry scope 和行业库 ID 调用 service。
func TestIndustryKnowledgeGetRAGFlowDatasetRoutesToService(t *testing.T) {
	stub := &industryKnowledgeServiceStub{ragflowDatasetResult: service.KnowledgeRAGFlowDatasetInfoResult{
		Scope: service.KnowledgeRAGFlowScopeIndustry, TargetID: "industry-1", TargetName: "保险", Status: "ok",
	}}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/industry-knowledge-bases/industry-1/ragflow-dataset", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, service.KnowledgeRAGFlowScopeIndustry, stub.ragflowDatasetScope)
	assert.Equal(t, "industry-1", stub.ragflowDatasetTargetID)
	assert.Contains(t, w.Body.String(), `"status":"ok"`)
}

// TestIndustryKnowledgePatchEmbeddingModelBindsHumanModelName 验证行业库模型修改把人类可读模型名和 provider 透传给 service。
func TestIndustryKnowledgePatchEmbeddingModelBindsHumanModelName(t *testing.T) {
	stub := &industryKnowledgeServiceStub{updateEmbeddingModelResult: service.KnowledgeRAGFlowDatasetInfoResult{
		Scope: service.KnowledgeRAGFlowScopeIndustry, TargetID: "industry-1", TargetName: "保险", Status: "ok",
	}}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/industry-knowledge-bases/industry-1/ragflow-dataset/embedding-model", bytes.NewBufferString(`{"name":"BAAI/bge-m3","provider":"OpenAI-API-Compatible"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, service.KnowledgeRAGFlowScopeIndustry, stub.updateEmbeddingModelScope)
	assert.Equal(t, "industry-1", stub.updateEmbeddingModelTargetID)
	assert.Equal(t, "BAAI/bge-m3", stub.updateEmbeddingModelInput.Name)
	assert.Equal(t, "OpenAI-API-Compatible", stub.updateEmbeddingModelInput.Provider)
}

// TestIndustryKnowledgePatchEmbeddingModelRejectsBadJSON 验证行业库模型修改请求体不是合法 JSON 时返回统一模型名称错误文案。
func TestIndustryKnowledgePatchEmbeddingModelRejectsBadJSON(t *testing.T) {
	stub := &industryKnowledgeServiceStub{}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/industry-knowledge-bases/industry-1/ragflow-dataset/embedding-model", bytes.NewBufferString(`{"name":`))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"BAD_REQUEST"`)
	assert.Contains(t, w.Body.String(), "模型名称不能为空")
	assert.Empty(t, stub.updateEmbeddingModelTargetID)
}
