package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
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

	listFilesResult     service.KnowledgeListResult
	listFilesErr        error
	listFilesIndustryID string
	listFilesStatus     string

	saveResult     service.KnowledgeDocumentResult
	saveErr        error
	saveCalls      int
	saveIndustryID string
	saveFilename   string
	saveSize       int64
	saveContent    string

	openContent string
	openSize    int64
	openName    string
	openErr     error

	deleteFileErr        error
	deleteFileIndustryID string
	deleteFileDocumentID string

	reparseResult     service.KnowledgeDocumentResult
	reparseErr        error
	reparseIndustryID string
	reparseDocumentID string

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

func (s *industryKnowledgeServiceStub) ListIndustryFiles(_ context.Context, _ auth.Principal, industryID string, _, _ int32, _, status string) (service.KnowledgeListResult, error) {
	s.listFilesIndustryID = industryID
	s.listFilesStatus = status
	return s.listFilesResult, s.listFilesErr
}

func (s *industryKnowledgeServiceStub) SaveIndustryFile(_ context.Context, _ auth.Principal, industryID, filename string, content io.Reader, size int64) (service.KnowledgeDocumentResult, error) {
	s.saveCalls++
	s.saveIndustryID = industryID
	s.saveFilename = filename
	s.saveSize = size
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

func (s *industryKnowledgeServiceStub) ReparseIndustryFile(_ context.Context, _ auth.Principal, industryID, documentID string) (service.KnowledgeDocumentResult, error) {
	s.reparseIndustryID = industryID
	s.reparseDocumentID = documentID
	return s.reparseResult, s.reparseErr
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
func newIndustryKnowledgeTestRouter(t *testing.T, svc industryKnowledgeService, uploadToken string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	handler := NewIndustryKnowledgeHandler(svc, uploadToken)
	RegisterExternalIndustryKnowledgeRoutes(router, handler)
	RegisterIndustryKnowledgeRoutes(router, handler)
	return router
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
