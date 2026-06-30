package handlers

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"oc-manager/internal/service"
)

type stubPublishService struct {
	gotToken string
	res      service.PublishResult
	err      error
}

func (s *stubPublishService) Publish(_ context.Context, token string, _ io.Reader) (service.PublishResult, error) {
	s.gotToken = token
	return s.res, s.err
}

// multipartTar 构造含 file 字段的 multipart 请求体，用于模拟 oc-publish 上传（不再带 slug 字段）。
func multipartTar(t *testing.T) (*bytes.Buffer, string) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "site.tar.gz")
	_, _ = fw.Write([]byte("\x1f\x8b\x08\x00")) // gzip 魔数占位（service 被 stub）
	_ = w.Close()
	return &buf, w.FormDataContentType()
}

// TestRuntimePublishHappy 覆盖：带 token + multipart → 调 service、返回 {url,expires_at}。
func TestRuntimePublishHappy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &stubPublishService{res: service.PublishResult{URL: "https://blog.apps.example.com", ExpiresAt: time.Now().Add(7 * 24 * time.Hour)}}
	r := gin.New()
	RegisterRuntimeWebPublishRoutes(r, NewRuntimeWebPublishHandler(svc))
	body, ct := multipartTar(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runtime/web-publish", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("X-OC-App-Token", "app-token-xyz")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "app-token-xyz", svc.gotToken)
	assert.Contains(t, w.Body.String(), "blog.apps.example.com")
}

// TestRuntimePublishMissingToken 覆盖：缺 X-OC-App-Token → 401。
func TestRuntimePublishMissingToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRuntimeWebPublishRoutes(r, NewRuntimeWebPublishHandler(&stubPublishService{}))
	body, ct := multipartTar(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runtime/web-publish", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
