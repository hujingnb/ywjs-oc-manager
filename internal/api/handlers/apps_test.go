package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// appsStub 实现 appService 接口，仅 stub 测试用到的方法。
type appsStub struct {
	getResult                  service.AppResult
	getErr                     error
	listResult                 []service.AppResult
	listErr                    error
	switchVersionResult        service.AppResult
	switchVersionErr           error
	updateLocaleResult         service.AppResult
	updateLocaleErr            error
	lastLocale                 string
	localeStatusResult         service.AppLocaleStatusResult
	localeStatusErr            error
}

func (s *appsStub) Get(_ context.Context, _ auth.Principal, _ string) (service.AppResult, error) {
	return s.getResult, s.getErr
}

func (s *appsStub) ListByOrg(_ context.Context, _ auth.Principal, _ string, _, _ int32) ([]service.AppResult, error) {
	return s.listResult, s.listErr
}

// SwitchAppVersion 实现 appService 接口的切换版本方法，返回预设结果。
func (s *appsStub) SwitchAppVersion(_ context.Context, _ auth.Principal, _, _ string) (service.AppResult, error) {
	return s.switchVersionResult, s.switchVersionErr
}

// UpdateAppLocale 实现 appService 接口的语言更新方法，记录请求语言便于断言。
func (s *appsStub) UpdateAppLocale(_ context.Context, _ auth.Principal, _ string, locale string) (service.AppResult, error) {
	s.lastLocale = locale
	if s.updateLocaleErr != nil {
		return service.AppResult{}, s.updateLocaleErr
	}
	return s.updateLocaleResult, nil
}

// AppLocaleStatus 实现 appService 接口的语言状态查询方法，返回预设结果。
func (s *appsStub) AppLocaleStatus(_ context.Context, _ auth.Principal, _ string) (service.AppLocaleStatusResult, error) {
	return s.localeStatusResult, s.localeStatusErr
}

// newAppsTestRouter 构建用于测试的 gin router。
func newAppsTestRouter(t *testing.T, svc appService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterAppRoutes(router, NewAppsHandler(svc))
	return router
}

// TestAppsListHappy 验证应用列表成功路径的成功路径场景。
func TestAppsListHappy(t *testing.T) {
	stub := &appsStub{listResult: []service.AppResult{{ID: "app-1", Name: "测试应用"}}}
	router := newAppsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/apps", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "app-1")
}

// TestAppsListForbidden 验证应用列表禁止访问的异常或拒绝路径场景。
func TestAppsListForbidden(t *testing.T) {
	stub := &appsStub{listErr: service.ErrForbidden}
	router := newAppsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-2/apps", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestAppsGetHappy 验证应用获取成功路径的成功路径场景。
func TestAppsGetHappy(t *testing.T) {
	stub := &appsStub{getResult: service.AppResult{ID: "app-1", Name: "测试应用"}}
	router := newAppsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "app-1")
}

// TestAppsGetNotFound 验证应用获取未找到的异常或拒绝路径场景。
func TestAppsGetNotFound(t *testing.T) {
	stub := &appsStub{getErr: service.ErrNotFound}
	router := newAppsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/missing", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestSwitchVersionHappy 验证切换版本成功路径：返回 200 且响应体包含更新后的实例 id。
func TestSwitchVersionHappy(t *testing.T) {
	// stub 预设切换成功，返回已绑定新版本的实例。
	stub := &appsStub{
		switchVersionResult: service.AppResult{ID: "app-1", VersionID: "ver-1"},
	}
	router := newAppsTestRouter(t, stub)

	body := strings.NewReader(`{"version_id":"ver-1"}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/version", body)
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	// 响应体中应包含实例 id，确认 app 字段被正常返回。
	assert.Contains(t, w.Body.String(), "app-1")
}

// TestSwitchVersionNotAllowed 验证目标版本不在 allowlist 内时返回 400 且错误码为 VERSION_NOT_ALLOWED。
func TestSwitchVersionNotAllowed(t *testing.T) {
	// stub 返回 ErrVersionNotInAllowlist，模拟 allowlist 校验失败。
	stub := &appsStub{switchVersionErr: service.ErrVersionNotInAllowlist}
	router := newAppsTestRouter(t, stub)

	body := strings.NewReader(`{"version_id":"ver-outside-allowlist"}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/version", body)
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	// allowlist 外的版本映射为 400，错误码为 VERSION_NOT_ALLOWED。
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "VERSION_NOT_ALLOWED")
}

// TestSwitchVersionBadRequest 验证请求体缺少 version_id 时返回 400。
func TestSwitchVersionBadRequest(t *testing.T) {
	// binding 校验失败，不依赖 stub 返回值。
	stub := &appsStub{}
	router := newAppsTestRouter(t, stub)

	// 请求体缺少必填字段 version_id，ShouldBindJSON 应返回绑定错误。
	body := strings.NewReader(`{}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/version", body)
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	// 必填字段缺失映射为 400，错误码为 INVALID_REQUEST。
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "INVALID_REQUEST")
}

// TestUpdateLocaleHappy 验证 PATCH /apps/:appId/locale 的成功路径：语言持久化后返回 200 及更新后实例。
func TestUpdateLocaleHappy(t *testing.T) {
	// stub 返回已更新 locale 字段的实例视图。
	stub := &appsStub{updateLocaleResult: service.AppResult{ID: "app-1", Locale: "zh"}}
	router := newAppsTestRouter(t, stub)

	body := strings.NewReader(`{"locale":"zh"}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/apps/app-1/locale", body)
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	// 成功路径：200 + app 结构体 + locale 字段已更新。
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"zh"`)
	assert.Equal(t, "zh", stub.lastLocale, "stub 应记录传入的 locale")
}

// TestUpdateLocaleInvalidLocale 验证不支持的语言代码返回 400 且错误码为 INVALID_LOCALE。
func TestUpdateLocaleInvalidLocale(t *testing.T) {
	// stub 返回 ErrInvalidLocale，模拟 service 层校验失败。
	stub := &appsStub{updateLocaleErr: service.ErrInvalidLocale}
	router := newAppsTestRouter(t, stub)

	body := strings.NewReader(`{"locale":"fr"}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/apps/app-1/locale", body)
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	// 不支持的语言映射为 400，错误码为 INVALID_LOCALE。
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "INVALID_LOCALE")
}

// TestLocaleStatusHappy 验证 GET /apps/:appId/locale-status 成功路径：
// 实例可达时 current_language 实时返回，且 needs_restart 透传 service 结果。
func TestLocaleStatusHappy(t *testing.T) {
	// stub 返回 current=zh、desired=en、needs_restart=true（运行中实例语言漂移）。
	cur := "zh"
	stub := &appsStub{localeStatusResult: service.AppLocaleStatusResult{
		CurrentLanguage: &cur,
		DesiredLanguage: "en",
		NeedsRestart:    true,
	}}
	router := newAppsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/locale-status", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"current_language":"zh"`)
	assert.Contains(t, w.Body.String(), `"desired_language":"en"`)
	assert.Contains(t, w.Body.String(), `"needs_restart":true`)
}

// TestLocaleStatusUnreachable 验证实例不可达时 current_language 序列化为 null（非省略）。
func TestLocaleStatusUnreachable(t *testing.T) {
	// stub 返回 current=nil（实例不可达），desired 仍存在。
	stub := &appsStub{localeStatusResult: service.AppLocaleStatusResult{
		CurrentLanguage: nil,
		DesiredLanguage: "en",
		NeedsRestart:    false,
	}}
	router := newAppsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/locale-status", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	// current_language 必须显式为 null，前端据此判断实例未运行。
	assert.Contains(t, w.Body.String(), `"current_language":null`)
}

// TestLocaleStatusForbidden 验证无权访问时返回 403。
func TestLocaleStatusForbidden(t *testing.T) {
	stub := &appsStub{localeStatusErr: service.ErrForbidden}
	router := newAppsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/locale-status", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestUpdateLocaleBadRequest 验证请求体缺少 locale 字段时返回 400。
func TestUpdateLocaleBadRequest(t *testing.T) {
	// binding 校验失败，不依赖 stub 返回值。
	stub := &appsStub{}
	router := newAppsTestRouter(t, stub)

	// 请求体缺少必填字段 locale，ShouldBindJSON 应返回绑定错误。
	body := strings.NewReader(`{}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/apps/app-1/locale", body)
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	// 必填字段缺失映射为 400，错误码为 INVALID_REQUEST。
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "INVALID_REQUEST")
}
