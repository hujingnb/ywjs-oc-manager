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
	// err 是 List() 的预设失败返回值（非 nil 时 List/Detail 返回错误）。
	err error
	// detail/versions 是 Detail() 的预设返回值。
	detail   service.SkillDetailResult
	versions []service.SkillVersionResult
	// downloadData/downloadExt/downloadErr 是 Download() 的预设返回值。
	downloadData []byte
	downloadExt  string
	downloadErr  error
}

// List 实现 skillMarketService，返回预设的 SkillPage 或错误。
func (s *skillMarketServiceStub) List(_ context.Context, _ auth.Principal, _, _, _ string) (service.SkillPage, error) {
	return s.page, s.err
}

// Detail 实现 skillMarketService，返回预设的详情与版本列表或错误。
func (s *skillMarketServiceStub) Detail(_ context.Context, _ auth.Principal, _, _ string) (service.SkillDetailResult, []service.SkillVersionResult, error) {
	return s.detail, s.versions, s.err
}

// Download 实现 skillMarketService，返回预设的归档字节/扩展名或错误。
func (s *skillMarketServiceStub) Download(_ context.Context, _ auth.Principal, _, _, _ string) ([]byte, string, error) {
	return s.downloadData, s.downloadExt, s.downloadErr
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

// TestSkillMarketHandler_Detail_OK 验证详情正常路径：
// service 返回详情 + 版本时，handler 响应 200 并以 "detail"/"versions" key 包装。
func TestSkillMarketHandler_Detail_OK(t *testing.T) {
	stub := &skillMarketServiceStub{
		detail:   service.SkillDetailResult{Name: "Self-Improving Agent", Source: "clawhub", Stars: 3735},
		versions: []service.SkillVersionResult{{Version: "3.0.21", Changelog: "re-upload"}},
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillMarketRoutes(r, NewSkillMarketHandler(stub))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/skill-market/detail?source=clawhub&ref=self-improving-agent", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, withPrincipal(req, auth.Principal{UserID: "u1"}))

	// 正常返回 200，响应体含 detail 与 versions。
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"detail"`)
	assert.Contains(t, rec.Body.String(), `"versions"`)
	assert.Contains(t, rec.Body.String(), `"3.0.21"`)
	assert.Contains(t, rec.Body.String(), `"re-upload"`)
}

// TestSkillMarketHandler_Detail_UnknownSource 未知来源时 Detail 应 400。
func TestSkillMarketHandler_Detail_UnknownSource(t *testing.T) {
	stub := &skillMarketServiceStub{err: service.ErrSkillMarketSourceUnknown}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillMarketRoutes(r, NewSkillMarketHandler(stub))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/skill-market/detail?source=github&ref=x", nil)
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

// TestSkillMarketHandler_Download_OK 验证下载正常路径：
// service 返回归档字节与 ext=tar 时，handler 响应 200，带 Content-Disposition 附件头与正确 Content-Type，body 为原始字节。
func TestSkillMarketHandler_Download_OK(t *testing.T) {
	stub := &skillMarketServiceStub{downloadData: []byte("TAR-BYTES"), downloadExt: "tar"}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillMarketRoutes(r, NewSkillMarketHandler(stub))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/skill-market/download?source=platform&ref=weather&version=1.0", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, withPrincipal(req, auth.Principal{UserID: "u1"}))

	// 200 + 原始字节 + 附件文件名 weather-1.0.tar + tar Content-Type。
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "TAR-BYTES", rec.Body.String())
	assert.Contains(t, rec.Header().Get("Content-Disposition"), `filename="weather-1.0.tar"`)
	assert.Equal(t, "application/x-tar", rec.Header().Get("Content-Type"))
}

// TestSkillMarketHandler_Download_ClawHubZip 验证 clawhub 下载返回 zip 文件名与 Content-Type。
func TestSkillMarketHandler_Download_ClawHubZip(t *testing.T) {
	stub := &skillMarketServiceStub{downloadData: []byte("ZIP"), downloadExt: "zip"}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillMarketRoutes(r, NewSkillMarketHandler(stub))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/skill-market/download?source=clawhub&ref=self-improving&version=3.0", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, withPrincipal(req, auth.Principal{UserID: "u1"}))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Disposition"), `filename="self-improving-3.0.zip"`)
	assert.Equal(t, "application/zip", rec.Header().Get("Content-Type"))
}

// TestSkillMarketHandler_Download_Denied 验证 service 返回 Denied 时 handler 响应 403。
func TestSkillMarketHandler_Download_Denied(t *testing.T) {
	stub := &skillMarketServiceStub{downloadErr: service.ErrSkillMarketDenied}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillMarketRoutes(r, NewSkillMarketHandler(stub))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/skill-market/download?source=platform&ref=weather&version=1.0", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, withPrincipal(req, auth.Principal{UserID: "u1"}))

	// 无权下载映射为 403。
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// TestSkillMarketHandler_Download_Invalid 验证 service 返回 Invalid 时 handler 响应 400。
func TestSkillMarketHandler_Download_Invalid(t *testing.T) {
	stub := &skillMarketServiceStub{downloadErr: service.ErrSkillMarketInvalid}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillMarketRoutes(r, NewSkillMarketHandler(stub))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/skill-market/download?source=platform&ref=weather", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, withPrincipal(req, auth.Principal{UserID: "u1"}))

	// 入参非法映射为 400。
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
