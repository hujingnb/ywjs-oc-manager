package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/service"
)

// fakeSyncService 实现 syncService，按预置数据或错误应答。
// listErr 不为 nil 时 ListActiveSitesForSync 返回错误；否则返回 records 切片。
type fakeSyncService struct {
	records []service.SiteSyncRecord
	listErr error
}

// ListActiveSitesForSync 如果 listErr 已设置则返回错误，否则返回预置 records。
func (f *fakeSyncService) ListActiveSitesForSync(_ context.Context) ([]service.SiteSyncRecord, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.records, nil
}

// newSyncTestRouter 构造仅挂 /internal/web-publish/sites 路由的 gin engine，
// 不挂用户鉴权中间件，与生产装配方式保持一致。
func newSyncTestRouter(svc syncService, token string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterInternalWebPublishRoutes(r, NewInternalWebPublishHandler(svc, token))
	return r
}

// TestSyncEndpointRejectsBadToken 验证 token 不匹配时返回 401，
// 防止未授权客户端读取站点路由信息。
func TestSyncEndpointRejectsBadToken(t *testing.T) {
	// 子测试：token 完全错误应返回 401。
	t.Run("错误 token 返回 401", func(t *testing.T) {
		r := newSyncTestRouter(&fakeSyncService{}, "correct-token")
		req := httptest.NewRequest(http.MethodGet, "/internal/web-publish/sites", nil)
		req.Header.Set("X-OC-Site-Sync-Token", "wrong-token")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	// 子测试：未携带 token header 应返回 401。
	t.Run("缺少 token header 返回 401", func(t *testing.T) {
		r := newSyncTestRouter(&fakeSyncService{}, "correct-token")
		req := httptest.NewRequest(http.MethodGet, "/internal/web-publish/sites", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	// 子测试：服务端 token 为空时，即使客户端传任意 token 也应返回 401（防止未配置时开放端点）。
	t.Run("服务端 token 为空时一律 401", func(t *testing.T) {
		r := newSyncTestRouter(&fakeSyncService{}, "")
		req := httptest.NewRequest(http.MethodGet, "/internal/web-publish/sites", nil)
		req.Header.Set("X-OC-Site-Sync-Token", "any-token")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

// TestSyncEndpointReturnsSites 验证正确 token 时返回 200，
// 响应体包含 sites 字段且包含预置站点的 host 信息，契约与 site-server SiteRecord 对齐。
func TestSyncEndpointReturnsSites(t *testing.T) {
	// 预置两条 active 站点记录，验证映射正确性。
	svc := &fakeSyncService{
		records: []service.SiteSyncRecord{
			{Host: "demo.example.com", SiteID: "site-001", S3Prefix: "published-sites/site-001/v1/", Status: "active"},
			{Host: "test.example.com", SiteID: "site-002", S3Prefix: "published-sites/site-002/v3/", Status: "active"},
		},
	}
	r := newSyncTestRouter(svc, "secret-token")
	req := httptest.NewRequest(http.MethodGet, "/internal/web-publish/sites", nil)
	req.Header.Set("X-OC-Site-Sync-Token", "secret-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// 验证 HTTP 状态码与响应体结构符合同步契约。
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	// 响应体必须包含顶层 sites 字段。
	assert.Contains(t, body, "sites")
	// 响应体必须包含预置站点的 host，验证映射未丢失数据。
	assert.Contains(t, body, "demo.example.com")
	assert.Contains(t, body, "site-001")
	assert.Contains(t, body, "published-sites/site-001/v1/")
}

// TestSyncEndpointServiceError 验证 service 层返回错误时 handler 返回 500，
// 不泄露具体内部错误信息。
func TestSyncEndpointServiceError(t *testing.T) {
	// service 返回内部错误，handler 应透传 500。
	svc := &fakeSyncService{listErr: errors.New("db connection failed")}
	r := newSyncTestRouter(svc, "secret-token")
	req := httptest.NewRequest(http.MethodGet, "/internal/web-publish/sites", nil)
	req.Header.Set("X-OC-Site-Sync-Token", "secret-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
