package handlers

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/service"
)

type runtimeKnowledgeServiceStub struct {
	searchCalls int
	searchToken string
	searchQuery string
	searchTopK  int32
	searchErr   error
	addCalls    int
	addToken    string
	addFilename string
	addBody     string
	addSize     int64
	addErr      error
}

func (s *runtimeKnowledgeServiceStub) RuntimeSearch(_ context.Context, appToken, question string, topK int32) (service.KnowledgeSearchResult, error) {
	s.searchCalls++
	s.searchToken = appToken
	s.searchQuery = question
	s.searchTopK = topK
	return service.KnowledgeSearchResult{Results: []service.KnowledgeSearchHit{{Scope: "app", Content: "命中"}}}, s.searchErr
}

func (s *runtimeKnowledgeServiceStub) RuntimeAddFile(_ context.Context, appToken, filename string, content io.Reader, size int64) (service.KnowledgeDocumentResult, error) {
	body, _ := io.ReadAll(content)
	s.addCalls++
	s.addToken = appToken
	s.addFilename = filename
	s.addBody = string(body)
	s.addSize = size
	return service.KnowledgeDocumentResult{ID: "doc-1", Name: filename, ParseStatus: "queued"}, s.addErr
}

func newRuntimeKnowledgeRouter(t *testing.T, svc runtimeKnowledgeService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterRuntimeKnowledgeRoutes(router, NewRuntimeKnowledgeHandler(svc))
	return router
}

// TestRuntimeKnowledgeSearchRequiresAppToken 验证 runtime 检索缺少 app token 时拒绝，且不触发 service。
func TestRuntimeKnowledgeSearchRequiresAppToken(t *testing.T) {
	stub := &runtimeKnowledgeServiceStub{}
	router := newRuntimeKnowledgeRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runtime/knowledge/search", bytes.NewBufferString(`{"question":"退款政策"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, 0, stub.searchCalls)
}

// TestRuntimeKnowledgeSearchDoesNotAcceptDatasetID 验证 handler 不把请求体中的 dataset_id 之类目标参数传给 service。
func TestRuntimeKnowledgeSearchDoesNotAcceptDatasetID(t *testing.T) {
	stub := &runtimeKnowledgeServiceStub{}
	router := newRuntimeKnowledgeRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runtime/knowledge/search", bytes.NewBufferString(`{"dataset_id":"evil","question":"退款政策","top_k":8}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(runtimeKnowledgeTokenHeader, "app-token")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, stub.searchCalls)
	assert.Equal(t, "app-token", stub.searchToken)
	assert.Equal(t, "退款政策", stub.searchQuery)
	assert.Equal(t, int32(8), stub.searchTopK)
}

// TestRuntimeKnowledgeAddAcceptsWorkspaceFileUpload 验证 runtime 文件写入接受 multipart file 并透传文件名与内容。
func TestRuntimeKnowledgeAddAcceptsWorkspaceFileUpload(t *testing.T) {
	stub := &runtimeKnowledgeServiceStub{}
	router := newRuntimeKnowledgeRouter(t, stub)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "report.md")
	require.NoError(t, err)
	_, err = part.Write([]byte("# report"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runtime/knowledge/files", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set(runtimeKnowledgeTokenHeader, "app-token")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, 1, stub.addCalls)
	assert.Equal(t, "app-token", stub.addToken)
	assert.Equal(t, "report.md", stub.addFilename)
	assert.Equal(t, "# report", stub.addBody)
	assert.Contains(t, w.Body.String(), `"parse_status":"queued"`)
}

// TestRuntimeKnowledgeAddRejectsOversizedUpload 验证 runtime API 在调用 service 前拒绝超过上限的上传。
func TestRuntimeKnowledgeAddRejectsOversizedUpload(t *testing.T) {
	stub := &runtimeKnowledgeServiceStub{}
	router := newRuntimeKnowledgeRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runtime/knowledge/files", bytes.NewBufferString("x"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	req.Header.Set("Content-Length", strconv.FormatInt(maxKnowledgeUploadBytes+maxKnowledgeMultipartOverheadBytes+1, 10))
	req.Header.Set(runtimeKnowledgeTokenHeader, "app-token")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	// 超限提示已接入 i18n catalog（MsgKnowledgeFileTooLarge），响应中应含 MB 数值。
	assert.Contains(t, w.Body.String(), strconv.FormatInt(maxKnowledgeUploadMB, 10))
	assert.Equal(t, 0, stub.addCalls)
}

// TestRuntimeKnowledgeAddMapsAICCOperationForbidden 验证 AICC runtime 的写入拒绝在 HTTP 层返回明确的
// 403 错误码，避免容器把该权限错误误判为可重试的 RAGFlow 异常。
func TestRuntimeKnowledgeAddMapsAICCOperationForbidden(t *testing.T) {
	stub := &runtimeKnowledgeServiceStub{addErr: service.ErrAICCOperationForbidden}
	router := newRuntimeKnowledgeRouter(t, stub)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "report.md")
	require.NoError(t, err)
	_, err = part.Write([]byte("# report"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runtime/knowledge/files", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set(runtimeKnowledgeTokenHeader, "aicc-token")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, 1, stub.addCalls)
	assert.Contains(t, w.Body.String(), "AICC_OPERATION_FORBIDDEN")
}
