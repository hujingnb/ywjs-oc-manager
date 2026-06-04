package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// skillMarketServiceStub 实现 skillMarketService 接口，用于隔离 handler 层测试。
// 通过 page/err 字段控制 List() 的预设返回值。
type skillMarketServiceStub struct {
	// page 是 List() 的预设成功返回值。
	page service.SkillPage
	// err 是 List() 的预设失败返回值（非 nil 时 List/Versions 返回错误）。
	err error
	// versions 是 Versions() 的预设返回值。
	versions []string
}

// List 实现 skillMarketService，返回预设的 SkillPage 或错误。
func (s *skillMarketServiceStub) List(_ context.Context, _ auth.Principal, _, _, _ string) (service.SkillPage, error) {
	return s.page, s.err
}

// Versions 实现 skillMarketService，返回预设的版本列表或错误。
func (s *skillMarketServiceStub) Versions(_ context.Context, _ auth.Principal, _, _ string) ([]string, error) {
	return s.versions, s.err
}

// TestSkillMarketHandler_List_OK 验证正常路径：
// service 返回一页条目时，handler 响应 200 并以 "page" key 包装结果。
func TestSkillMarketHandler_List_OK(t *testing.T) {
	// 预设 service 返回含一条平台库条目的 SkillPage。
	stub := &skillMarketServiceStub{
		page: service.SkillPage{
			Entries: []service.SkillEntry{
				{Source: "platform", Name: "weather", Version: "1.0"},
			},
		},
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillMarketRoutes(r, NewSkillMarketHandler(stub))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/skill-market", nil)
	rec := httptest.NewRecorder()
	// 注入已认证 principal（模拟认证中间件已放行）。
	r.ServeHTTP(rec, withPrincipal(req, auth.Principal{UserID: "u1"}))

	// 正常返回 200，响应体包含 "page" 字段。
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"page"`)
	assert.Contains(t, rec.Body.String(), `"weather"`)
}

// TestSkillMarketHandler_Versions_OK 验证版本列表正常路径：
// service 返回版本切片时，handler 响应 200 并以 "versions" key 包装。
func TestSkillMarketHandler_Versions_OK(t *testing.T) {
	stub := &skillMarketServiceStub{versions: []string{"3.0.21", "3.0.20"}}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillMarketRoutes(r, NewSkillMarketHandler(stub))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/skill-market/versions?source=clawhub&ref=self-improving-agent", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, withPrincipal(req, auth.Principal{UserID: "u1"}))

	// 正常返回 200，响应体以 "versions" 包装版本号。
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"versions"`)
	assert.Contains(t, rec.Body.String(), `"3.0.21"`)
}

// TestSkillMarketHandler_Versions_UnknownSource 未知来源时 Versions 应 400。
func TestSkillMarketHandler_Versions_UnknownSource(t *testing.T) {
	stub := &skillMarketServiceStub{err: service.ErrSkillMarketSourceUnknown}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillMarketRoutes(r, NewSkillMarketHandler(stub))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/skill-market/versions?source=github&ref=x", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, withPrincipal(req, auth.Principal{UserID: "u1"}))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestSkillMarketHandler_List_UnknownSource 验证未知来源错误路径：
// service 返回 ErrSkillMarketSourceUnknown 时，handler 响应 400。
func TestSkillMarketHandler_List_UnknownSource(t *testing.T) {
	// 模拟 source=github 这类未知来源，service 返回哨兵错误。
	stub := &skillMarketServiceStub{
		err: service.ErrSkillMarketSourceUnknown,
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillMarketRoutes(r, NewSkillMarketHandler(stub))

	// 请求携带未知来源参数。
	req := httptest.NewRequest(http.MethodGet, "/api/v1/skill-market?source=github", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, withPrincipal(req, auth.Principal{UserID: "u1"}))

	// 未知来源应返回 400 Bad Request。
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestSkillMarketHandler_List_InternalError 验证服务器内部错误路径：
// service 返回非哨兵错误时，handler 响应 500。
func TestSkillMarketHandler_List_InternalError(t *testing.T) {
	// 模拟数据库或网络异常等非预期错误。
	stub := &skillMarketServiceStub{
		err: assert.AnError, // 使用 testify 内置非 nil 通用错误。
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillMarketRoutes(r, NewSkillMarketHandler(stub))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/skill-market", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, withPrincipal(req, auth.Principal{UserID: "u1"}))

	// 服务器内部错误应返回 500。
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
