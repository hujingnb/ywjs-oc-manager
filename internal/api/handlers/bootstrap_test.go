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
	"oc-manager/internal/store/sqlc"
)

// fakeBootstrapAppService 实现 BootstrapAppService，按预置 app 与构建结果应答。
// resolveErr 不为 nil 时 ResolveByControlToken 返回错误；buildErr 不为 nil 时 Build 返回错误。
type fakeBootstrapAppService struct {
	app        sqlc.App
	resolveErr error
	buildErr   error
}

// ResolveByControlToken 如果 resolveErr 已设置则返回错误，否则返回预置 app。
func (f *fakeBootstrapAppService) ResolveByControlToken(_ context.Context, _ string) (sqlc.App, error) {
	if f.resolveErr != nil {
		return sqlc.App{}, f.resolveErr
	}
	return f.app, nil
}

// Build 如果 buildErr 已设置则返回错误，否则返回固定 manifest 内容供断言使用。
func (f *fakeBootstrapAppService) Build(_ context.Context, _ sqlc.App) (service.BootstrapResult, error) {
	if f.buildErr != nil {
		return service.BootstrapResult{}, f.buildErr
	}
	return service.BootstrapResult{ManifestYAML: "app:\n  id: a1\n"}, nil
}

// newBootstrapTestRouter 构造仅挂 /internal/apps/:id/bootstrap 路由的 gin engine，
// 不挂用户鉴权中间件，与生产装配方式保持一致。
func newBootstrapTestRouter(svc BootstrapAppService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterBootstrapRoutes(r, NewBootstrapHandler(svc))
	return r
}

// TestBootstrapMissingToken 验证请求缺少 Authorization header 时返回 401，
// 阻止无凭证 pod 拉取任何 bootstrap 配置。
func TestBootstrapMissingToken(t *testing.T) {
	r := newBootstrapTestRouter(&fakeBootstrapAppService{app: sqlc.App{ID: "a1"}})
	req := httptest.NewRequest(http.MethodGet, "/internal/apps/a1/bootstrap", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestBootstrapTokenAppMismatch 验证 token 反查到 a1，但 path :id 为 a2 时返回 401，
// 防止持 A 的 token 横向拉取 B 的配置（越权场景）。
func TestBootstrapTokenAppMismatch(t *testing.T) {
	// fake 返回 app.ID="a1"，但请求 path 中目标为 a2，handler 应拒绝
	r := newBootstrapTestRouter(&fakeBootstrapAppService{app: sqlc.App{ID: "a1"}})
	req := httptest.NewRequest(http.MethodGet, "/internal/apps/a2/bootstrap", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestBootstrapNotReady 验证 app 未就绪（Build 返回 ErrAppNotReady）时返回 409，
// pod initContainer 应根据 409 稍后重试而非视为永久失败。
func TestBootstrapNotReady(t *testing.T) {
	r := newBootstrapTestRouter(&fakeBootstrapAppService{
		app:      sqlc.App{ID: "a1"},
		buildErr: service.ErrAppNotReady,
	})
	req := httptest.NewRequest(http.MethodGet, "/internal/apps/a1/bootstrap", nil)
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
}

// TestBootstrapHappyPath 验证 token 有效、app 匹配、Build 成功时返回 200，
// 并且响应体包含 manifest_yaml 字段。
func TestBootstrapHappyPath(t *testing.T) {
	r := newBootstrapTestRouter(&fakeBootstrapAppService{app: sqlc.App{ID: "a1"}})
	req := httptest.NewRequest(http.MethodGet, "/internal/apps/a1/bootstrap", nil)
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "manifest_yaml")
}

// TestBootstrapResolveError 验证 token 反查失败（如 DB 查无此记录）时返回 401，
// 不泄露目标 app 是否存在，防止 token 枚举探测。
func TestBootstrapResolveError(t *testing.T) {
	r := newBootstrapTestRouter(&fakeBootstrapAppService{resolveErr: errors.New("no rows")})
	req := httptest.NewRequest(http.MethodGet, "/internal/apps/a1/bootstrap", nil)
	req.Header.Set("Authorization", "Bearer bad")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
