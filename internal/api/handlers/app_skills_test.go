// Package handlers — app_skills_test.go 覆盖 AppSkillsHandler 的 HTTP 层行为，
// 验证路由注册、请求绑定、响应状态码和错误映射是否与 writeAppSkillError 规则一致。
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// appSkillServiceStub 实现 appSkillService 接口，供 handler 单测注入。
type appSkillServiceStub struct {
	// List 相关
	listResult []service.AppSkillResult
	listErr    error

	// Install 相关
	installResult service.AppSkillResult
	installErr    error

	// Uninstall 相关
	uninstallErr error

	// Update 相关
	updateResult service.AppSkillResult
	updateErr    error

	// Reinstall 相关
	reinstallResult service.AppSkillResult
	reinstallErr    error
}

// List 返回预设的 skill 列表结果或错误。
func (s *appSkillServiceStub) List(_ context.Context, _ auth.Principal, _ string) ([]service.AppSkillResult, error) {
	return s.listResult, s.listErr
}

// Install 返回预设的安装结果或错误。
func (s *appSkillServiceStub) Install(_ context.Context, _ auth.Principal, _ string, _ service.InstallSkillInput) (service.AppSkillResult, error) {
	return s.installResult, s.installErr
}

// Uninstall 返回预设的卸载错误（nil 表示成功）。
func (s *appSkillServiceStub) Uninstall(_ context.Context, _ auth.Principal, _, _ string) error {
	return s.uninstallErr
}

// Update 返回预设的更新结果或错误。
func (s *appSkillServiceStub) Update(_ context.Context, _ auth.Principal, _, _, _ string) (service.AppSkillResult, error) {
	return s.updateResult, s.updateErr
}

// Reinstall 返回预设的重装结果或错误。
func (s *appSkillServiceStub) Reinstall(_ context.Context, _ auth.Principal, _, _ string) (service.AppSkillResult, error) {
	return s.reinstallResult, s.reinstallErr
}

// newAppSkillsTestRouter 创建注册了 AppSkills 路由的测试用 gin.Engine。
func newAppSkillsTestRouter(t *testing.T, svc appSkillService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	RegisterAppSkillRoutes(r, NewAppSkillsHandler(svc))
	return r
}

// TestAppSkillsHandler_List_OK 验证列表接口返回 200 和 skills 数组（含 status 字段）。
func TestAppSkillsHandler_List_OK(t *testing.T) {
	// 正常场景：stub 返回两条已安装 skill
	stub := &appSkillServiceStub{
		listResult: []service.AppSkillResult{
			{Name: "weather", Source: "platform", Version: "1.0.0", Status: "active"},
			{Name: "search", Source: "clawhub", Version: "2.1.0", Status: "pending"},
		},
	}
	router := newAppSkillsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/skills", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	// 响应体必须包含 skills 数组及各 skill 的 name 与 status
	body := w.Body.String()
	assert.Contains(t, body, `"weather"`)
	assert.Contains(t, body, `"search"`)
	assert.Contains(t, body, `"active"`)
	assert.Contains(t, body, `"pending"`)
}

// TestAppSkillsHandler_List_Denied 验证无权限时列表接口返回 403。
func TestAppSkillsHandler_List_Denied(t *testing.T) {
	// 权限不足场景：stub 返回 ErrAppSkillDenied
	stub := &appSkillServiceStub{listErr: service.ErrAppSkillDenied}
	router := newAppSkillsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/skills", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestAppSkillsHandler_Install_OK 验证安装接口在成功时返回 201 及安装结果。
func TestAppSkillsHandler_Install_OK(t *testing.T) {
	// 正常安装场景：source=platform，service 返回 active skill
	stub := &appSkillServiceStub{
		installResult: service.AppSkillResult{
			Name: "weather", Source: "platform", SourceRef: "weather", Version: "1.0.0", Status: "active",
		},
	}
	router := newAppSkillsTestRouter(t, stub)

	body, _ := json.Marshal(map[string]string{
		"source":     "platform",
		"source_ref": "weather",
		"name":       "weather",
		"version":    "1.0.0",
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/skills", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	assert.Contains(t, w.Body.String(), `"weather"`)
	assert.Contains(t, w.Body.String(), `"active"`)
}

// TestAppSkillsHandler_Install_NameConflict 验证同名 skill 重复安装映射为 409。
func TestAppSkillsHandler_Install_NameConflict(t *testing.T) {
	// 已有同名 skill 场景：service 返回 ErrAppSkillNameConflict
	stub := &appSkillServiceStub{installErr: service.ErrAppSkillNameConflict}
	router := newAppSkillsTestRouter(t, stub)

	body, _ := json.Marshal(map[string]string{
		"source":     "platform",
		"source_ref": "weather",
		"name":       "weather",
		"version":    "1.0.0",
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/skills", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}

// TestAppSkillsHandler_Install_SourceUnknown 验证未知 skill 来源映射为 400。
func TestAppSkillsHandler_Install_SourceUnknown(t *testing.T) {
	// 未知来源场景：service 返回 ErrAppSkillSourceUnknown
	stub := &appSkillServiceStub{installErr: service.ErrAppSkillSourceUnknown}
	router := newAppSkillsTestRouter(t, stub)

	body, _ := json.Marshal(map[string]string{
		"source": "unknown", "source_ref": "x", "name": "x", "version": "1.0.0",
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/skills", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestAppSkillsHandler_Install_ArchiveTooDangerous 验证归档解压炸弹检测映射为 400。
func TestAppSkillsHandler_Install_ArchiveTooDangerous(t *testing.T) {
	// 归档炸弹场景：service 返回 ErrAppSkillArchiveTooDangerous
	stub := &appSkillServiceStub{installErr: service.ErrAppSkillArchiveTooDangerous}
	router := newAppSkillsTestRouter(t, stub)

	body, _ := json.Marshal(map[string]string{
		"source": "clawhub", "source_ref": "bomb", "name": "bomb", "version": "1.0.0",
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/skills", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestAppSkillsHandler_Uninstall_OK 验证卸载成功时返回 204 无响应体。
func TestAppSkillsHandler_Uninstall_OK(t *testing.T) {
	// 正常卸载场景：service 返回 nil
	stub := &appSkillServiceStub{}
	router := newAppSkillsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/apps/app-1/skills/weather", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.String())
}

// TestAppSkillsHandler_Uninstall_Protected 验证内置保护 skill 卸载被拒映射为 403。
func TestAppSkillsHandler_Uninstall_Protected(t *testing.T) {
	// 删除保护场景：service 返回 ErrAppSkillProtected → HTTP 403
	stub := &appSkillServiceStub{uninstallErr: service.ErrAppSkillProtected}
	router := newAppSkillsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/apps/app-1/skills/builtin-skill", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "APP_SKILL_PROTECTED")
}

// TestAppSkillsHandler_Uninstall_NotFound 验证卸载不存在的 skill 映射为 404。
func TestAppSkillsHandler_Uninstall_NotFound(t *testing.T) {
	// 不存在场景：service 返回 ErrAppSkillNotFound → HTTP 404
	stub := &appSkillServiceStub{uninstallErr: service.ErrAppSkillNotFound}
	router := newAppSkillsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/apps/app-1/skills/ghost", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestAppSkillsHandler_Update_OK 验证更新接口成功时返回 200 及更新后的 skill 结果。
func TestAppSkillsHandler_Update_OK(t *testing.T) {
	// 正常更新场景：目标版本 2.0.0，service 返回更新成功的 skill
	stub := &appSkillServiceStub{
		updateResult: service.AppSkillResult{
			Name: "weather", Source: "platform", Version: "2.0.0", Status: "active",
		},
	}
	router := newAppSkillsTestRouter(t, stub)

	body, _ := json.Marshal(map[string]string{"version": "2.0.0"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/skills/weather/update", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"2.0.0"`)
}

// TestAppSkillsHandler_Update_NotFound 验证更新不存在的 skill 映射为 404。
func TestAppSkillsHandler_Update_NotFound(t *testing.T) {
	// 不存在场景：service 返回 ErrAppSkillNotFound → HTTP 404
	stub := &appSkillServiceStub{updateErr: service.ErrAppSkillNotFound}
	router := newAppSkillsTestRouter(t, stub)

	body, _ := json.Marshal(map[string]string{"version": "2.0.0"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/skills/ghost/update", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestAppSkillsHandler_ErrorMapping 验证 writeAppSkillError 各哨兵错误与 HTTP 状态码的映射关系。
func TestAppSkillsHandler_ErrorMapping(t *testing.T) {
	// table-driven：每行覆盖一种哨兵错误 → HTTP 状态码的映射
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		// ErrAppSkillDenied → 403（无权操作）
		{"denied", service.ErrAppSkillDenied, http.StatusForbidden, "APP_SKILL_DENIED"},
		// ErrAppSkillNotFound → 404（skill 不存在）
		{"not_found", service.ErrAppSkillNotFound, http.StatusNotFound, "APP_SKILL_NOT_FOUND"},
		// ErrAppSkillNameConflict → 409（同名 skill 已安装）
		{"name_conflict", service.ErrAppSkillNameConflict, http.StatusConflict, "APP_SKILL_NAME_CONFLICT"},
		// ErrAppSkillProtected → 403（版本内置 skill 不可删除）
		{"protected", service.ErrAppSkillProtected, http.StatusForbidden, "APP_SKILL_PROTECTED"},
		// ErrAppSkillSourceUnknown → 400（未知来源）
		{"source_unknown", service.ErrAppSkillSourceUnknown, http.StatusBadRequest, "APP_SKILL_SOURCE_UNKNOWN"},
		// ErrAppSkillArchiveTooDangerous → 400（归档炸弹）
		{"archive_too_dangerous", service.ErrAppSkillArchiveTooDangerous, http.StatusBadRequest, "APP_SKILL_ARCHIVE_TOO_DANGEROUS"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// 利用 Uninstall 接口触发各种 service 错误，验证 writeAppSkillError 映射
			stub := &appSkillServiceStub{uninstallErr: tc.err}
			router := newAppSkillsTestRouter(t, stub)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodDelete, "/api/v1/apps/app-1/skills/skill-x", nil)
			req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
			router.ServeHTTP(w, req)

			assert.Equal(t, tc.wantStatus, w.Code, "期望 HTTP 状态码")
			assert.Contains(t, w.Body.String(), tc.wantCode, "期望错误码出现在响应体中")
		})
	}
}

// TestAppSkillsHandler_Install_UpstreamUnavailable 验证安装时上游下载失败映射为 502。
func TestAppSkillsHandler_Install_UpstreamUnavailable(t *testing.T) {
	// 上游故障场景：service 返回 ErrSkillMarketUpstreamUnavailable。
	stub := &appSkillServiceStub{installErr: service.ErrSkillMarketUpstreamUnavailable}
	router := newAppSkillsTestRouter(t, stub)

	body, _ := json.Marshal(map[string]string{
		"source": "clawhub", "source_ref": "self-improving", "name": "self-improving", "version": "1.2.16",
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/skills", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	// 上游故障映射为 502，文案明确，错误码加 APP_SKILL_ 命名前缀。
	// 文案改走 msgKey catalog：测试路由无 locale 中间件，LocaleFrom(nil) 回落 en，断言取 catalog en 译文。
	assert.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), apierror.Localize(apierror.MsgAppSkillUpstreamUnavailable, apierror.LocaleFrom(nil)))
	assert.Contains(t, w.Body.String(), "APP_SKILL_UPSTREAM_UNAVAILABLE")
}
