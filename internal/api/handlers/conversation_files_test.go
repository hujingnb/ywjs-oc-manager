package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// stubConversationFileService 实现 handler 依赖的窄接口，用于单测注入。
type stubConversationFileService struct {
	// uploadResult 上传成功时返回的元数据。
	uploadResult service.ConversationFileUploadResult
	// downloadURL 下载成功时返回的预签名 URL。
	downloadURL string
	// downloadFilename 下载成功时返回的原始文件名。
	downloadFilename string
	// err 控制两个方法均返回的错误（nil 表示成功路径）。
	err error
}

// Upload 返回预设结果，便于测试各类上传场景。
func (s *stubConversationFileService) Upload(_ context.Context, _ auth.Principal, _, _, _ string, _ io.Reader, _ int64) (service.ConversationFileUploadResult, error) {
	return s.uploadResult, s.err
}

// Download 返回预设结果，便于测试各类下载场景。
func (s *stubConversationFileService) Download(_ context.Context, _ auth.Principal, _, _, _ string) (url, filename string, err error) {
	return s.downloadURL, s.downloadFilename, s.err
}

// newConvFileTestRouter 构造注入 fake service 的 gin 测试路由器。
// 与 hermes_conversation_test.go 中 newConvTestRouter 风格对齐。
func newConvFileTestRouter(svc conversationFileService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterHermesConversationFileRoutes(r, NewHermesConversationFileHandler(svc))
	return r
}

// 上传成功：fake svc 返回固定元数据，handler 应返回 200 且响应体含 file_id。
func TestConversationFileUploadSuccess(t *testing.T) {
	svc := &stubConversationFileService{
		uploadResult: service.ConversationFileUploadResult{
			FileID:   "f1",
			Filename: "doc.pdf",
			Mime:     "application/pdf",
			Size:     1024,
		},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/apps/app-1/hermes/conversations/sid-1/files?filename=doc.pdf",
		strings.NewReader("binary-content"))
	newConvFileTestRouter(svc).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	// 响应体应包含 file_id 字段，值为 fake svc 返回的 "f1"。
	assert.Contains(t, w.Body.String(), "f1")
	assert.Contains(t, w.Body.String(), "file_id")
}

// filename 缺失：query 中未提供 filename 参数，handler 应返回 400，响应体含 apierror code。
func TestConversationFileUploadMissingFilename(t *testing.T) {
	svc := &stubConversationFileService{}
	w := httptest.NewRecorder()
	// 故意不带 filename 查询参数，验证前置校验逻辑。
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/apps/app-1/hermes/conversations/sid-1/files",
		strings.NewReader("binary-content"))
	newConvFileTestRouter(svc).ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	// 响应体应为 apierror ErrorResponse 格式，包含 code 字段。
	assert.Contains(t, w.Body.String(), "CONVERSATION_FILE_BAD_REQUEST")
}

// 上传文件类型不支持：fake svc 返回 ErrConversationFileUnsupported，handler 应映射为 400。
func TestConversationFileUploadUnsupported(t *testing.T) {
	svc := &stubConversationFileService{err: service.ErrConversationFileUnsupported}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/apps/app-1/hermes/conversations/sid-1/files?filename=virus.exe",
		strings.NewReader("binary-content"))
	newConvFileTestRouter(svc).ServeHTTP(w, req)
	// ErrConversationFileUnsupported 应映射为 HTTP 400 Bad Request。
	require.Equal(t, http.StatusBadRequest, w.Code)
}

// 下载成功：fake svc 返回预签名 URL，handler 应返回 302 且 Location 头为该 URL。
func TestConversationFileDownloadSuccess(t *testing.T) {
	const presignedURL = "https://s3.example.com/files/f1?sig=abc"
	svc := &stubConversationFileService{
		downloadURL:      presignedURL,
		downloadFilename: "doc.pdf",
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/apps/app-1/hermes/conversations/sid-1/files/f1", nil)
	newConvFileTestRouter(svc).ServeHTTP(w, req)
	// handler 应以 302 重定向客户端至预签名 URL。
	require.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, presignedURL, w.Header().Get("Location"))
}

// 下载文件不存在：fake svc 返回 ErrConversationFileNotFound，handler 应映射为 404。
func TestConversationFileDownloadNotFound(t *testing.T) {
	svc := &stubConversationFileService{err: service.ErrConversationFileNotFound}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/apps/app-1/hermes/conversations/sid-1/files/missing", nil)
	newConvFileTestRouter(svc).ServeHTTP(w, req)
	// ErrConversationFileNotFound 应映射为 HTTP 404 Not Found。
	require.Equal(t, http.StatusNotFound, w.Code)
}
