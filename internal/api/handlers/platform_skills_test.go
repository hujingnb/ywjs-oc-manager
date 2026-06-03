package handlers

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// platformSkillServiceStub 实现 handler 依赖的接口，隔离 HTTP 层测试。
type platformSkillServiceStub struct {
	uploadRes service.PlatformSkillResult
	uploadErr error
	listRes   []service.PlatformSkillResult
	deleteErr error
	gotUpload service.PlatformSkillUploadInput
}

// List 返回预设的列表结果，用于测试列出平台库 skill 路径。
func (s *platformSkillServiceStub) List(context.Context, auth.Principal) ([]service.PlatformSkillResult, error) {
	return s.listRes, nil
}

// Upload 记录实际入参并返回预设结果，用于验证 handler 解析 multipart 字段是否正确。
func (s *platformSkillServiceStub) Upload(_ context.Context, _ auth.Principal, in service.PlatformSkillUploadInput) (service.PlatformSkillResult, error) {
	s.gotUpload = in
	return s.uploadRes, s.uploadErr
}

// Delete 返回预设错误，用于测试错误映射逻辑。
func (s *platformSkillServiceStub) Delete(context.Context, auth.Principal, string) error {
	return s.deleteErr
}

// platformAdminReq 构造携带平台管理员身份的请求，供平台库路由测试复用。
func platformAdminReq(req *http.Request) *http.Request {
	return withPrincipal(req, auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin})
}

// TestPlatformSkillsHandler_Upload 验证 POST multipart 上传：
// handler 正确解析 name/version/file 字段，并在成功时返回 201 与 service 结果。
func TestPlatformSkillsHandler_Upload(t *testing.T) {
	// 预设 service 返回一个合法 skill 结果
	stub := &platformSkillServiceStub{uploadRes: service.PlatformSkillResult{ID: "ps1", Name: "weather", Version: "1.0"}}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterPlatformSkillRoutes(r, NewPlatformSkillsHandler(stub))

	// 构造 multipart 请求体，包含 name/version 字段与 file 归档
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("name", "weather")
	_ = w.WriteField("version", "1.0")
	fw, _ := w.CreateFormFile("file", "weather.tar")
	_, _ = fw.Write([]byte("archive"))
	_ = w.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/platform-skills", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, platformAdminReq(req))

	// 上传成功应返回 201，且 service 收到正确入参
	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "weather", stub.gotUpload.Name)
	assert.Equal(t, "1.0", stub.gotUpload.Version)
	assert.Equal(t, []byte("archive"), stub.gotUpload.Data)
}

// TestPlatformSkillsHandler_Delete_NotFound 验证 DELETE 不存在的 skill 时
// handler 将 ErrPlatformSkillNotFound 正确映射为 404。
func TestPlatformSkillsHandler_Delete_NotFound(t *testing.T) {
	// service 返回 NotFound 哨兵错误
	stub := &platformSkillServiceStub{deleteErr: service.ErrPlatformSkillNotFound}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterPlatformSkillRoutes(r, NewPlatformSkillsHandler(stub))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/platform-skills/ps1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, platformAdminReq(req))
	// 不存在时应返回 404
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
