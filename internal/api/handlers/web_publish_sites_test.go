// Package handlers 的 web_publish_sites_test 覆盖站点管理与证书状态管理面接口的核心路径。
// 测试覆盖：
//   - ListByOrg：200 + 站点列表正确透传；
//   - Takedown：204 + service 被调用；
//   - Renew：204 + service 被调用；
//   - GetConfig：200 + 配置结果正确透传；
//   - RetryProvision：204 + service 被调用；
//   - ErrWebPublishNotProvisioned 映射为 403（通过 writeConfigServiceError）；
//   - service.ErrForbidden 映射为 403（通过 writeServiceError）。
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// ===== stub 实现 =====

// webPublishSiteServiceStub 是 webPublishSiteService 的测试替身。
type webPublishSiteServiceStub struct {
	listResult    []service.SiteResult
	listErr       error
	takedownErr   error
	renewErr      error
	lastPrincipal auth.Principal
	lastOrgID     string
	lastSiteID    string
	takedownCalls int
	renewCalls    int
}

func (s *webPublishSiteServiceStub) ListByOrg(_ context.Context, p auth.Principal, orgID string) ([]service.SiteResult, error) {
	s.lastPrincipal = p
	s.lastOrgID = orgID
	return s.listResult, s.listErr
}

func (s *webPublishSiteServiceStub) Takedown(_ context.Context, p auth.Principal, siteID string) error {
	s.lastPrincipal = p
	s.lastSiteID = siteID
	s.takedownCalls++
	return s.takedownErr
}

func (s *webPublishSiteServiceStub) Renew(_ context.Context, p auth.Principal, siteID string) error {
	s.lastPrincipal = p
	s.lastSiteID = siteID
	s.renewCalls++
	return s.renewErr
}

// webPublishConfigReadServiceStub 是 webPublishConfigReadService 的测试替身。
type webPublishConfigReadServiceStub struct {
	getResult         service.WebPublishConfigResult
	getErr            error
	retryErr          error
	lastPrincipal     auth.Principal
	lastOrgID         string
	retryProvisionCalls int
}

func (s *webPublishConfigReadServiceStub) Get(_ context.Context, p auth.Principal, orgID string) (service.WebPublishConfigResult, error) {
	s.lastPrincipal = p
	s.lastOrgID = orgID
	return s.getResult, s.getErr
}

func (s *webPublishConfigReadServiceStub) RetryProvision(_ context.Context, p auth.Principal, orgID string) error {
	s.lastPrincipal = p
	s.lastOrgID = orgID
	s.retryProvisionCalls++
	return s.retryErr
}

// newWebPublishSitesTestRouter 构造仅含站点管理路由的测试 gin 引擎。
func newWebPublishSitesTestRouter(
	t *testing.T,
	sites webPublishSiteService,
	config webPublishConfigReadService,
) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterWebPublishSiteRoutes(router, NewWebPublishSitesHandler(sites, config))
	return router
}

// 平台管理员 principal，供测试共用。
var platformAdminPrincipal = auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin}

// ===== ListByOrg 测试 =====

// TestWebPublishSitesListByOrgReturns200WithSites 验证 ListByOrg 正常路径：
// 返回 200 并在响应体 "sites" 字段携带 service 返回的站点列表。
func TestWebPublishSitesListByOrgReturns200WithSites(t *testing.T) {
	fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	sitesSvc := &webPublishSiteServiceStub{
		listResult: []service.SiteResult{
			{ID: "site-1", Host: "myapp.apps.example.com", URL: "https://myapp.apps.example.com", Slug: "myapp", Status: "active", SizeBytes: 1024, CreatedAt: fixedTime, ExpiresAt: fixedTime.Add(7 * 24 * time.Hour)},
		},
	}
	router := newWebPublishSitesTestRouter(t, sitesSvc, &webPublishConfigReadServiceStub{})

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/published-sites", nil)
	req = withPrincipal(req, platformAdminPrincipal)
	router.ServeHTTP(recorder, req)

	// 验证响应状态码为 200。
	require.Equal(t, http.StatusOK, recorder.Code)
	// 验证 service 收到正确的 orgID。
	assert.Equal(t, "org-1", sitesSvc.lastOrgID)
	// 验证响应体携带站点列表。
	var body struct {
		Sites []service.SiteResult `json:"sites"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Len(t, body.Sites, 1)
	assert.Equal(t, "site-1", body.Sites[0].ID)
	assert.Equal(t, "active", body.Sites[0].Status)
}

// TestWebPublishSitesListByOrgForwardsPrincipal 验证 ListByOrg 把认证主体正确透传给 service。
func TestWebPublishSitesListByOrgForwardsPrincipal(t *testing.T) {
	sitesSvc := &webPublishSiteServiceStub{listResult: []service.SiteResult{}}
	router := newWebPublishSitesTestRouter(t, sitesSvc, &webPublishConfigReadServiceStub{})

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-2/published-sites", nil)
	req = withPrincipal(req, platformAdminPrincipal)
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	// 验证 service 收到的 principal 与请求注入一致。
	assert.Equal(t, platformAdminPrincipal.UserID, sitesSvc.lastPrincipal.UserID)
}

// TestWebPublishSitesListByOrgMapsForbidden 验证 service 返回 ErrForbidden 时 handler 响应 403。
func TestWebPublishSitesListByOrgMapsForbidden(t *testing.T) {
	sitesSvc := &webPublishSiteServiceStub{listErr: service.ErrForbidden}
	router := newWebPublishSitesTestRouter(t, sitesSvc, &webPublishConfigReadServiceStub{})

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/published-sites", nil)
	req = withPrincipal(req, auth.Principal{UserID: "user-2", Role: domain.UserRoleOrgMember})
	router.ServeHTTP(recorder, req)

	// 无权限场景应返回 403。
	require.Equal(t, http.StatusForbidden, recorder.Code)
}

// ===== Takedown 测试 =====

// TestWebPublishSitesTakedownReturns204 验证 Takedown 正常路径：service 调用成功时返回 204。
func TestWebPublishSitesTakedownReturns204(t *testing.T) {
	sitesSvc := &webPublishSiteServiceStub{}
	router := newWebPublishSitesTestRouter(t, sitesSvc, &webPublishConfigReadServiceStub{})

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/published-sites/site-1/disable", nil)
	req = withPrincipal(req, platformAdminPrincipal)
	router.ServeHTTP(recorder, req)

	// 正常下线返回 204 No Content。
	require.Equal(t, http.StatusNoContent, recorder.Code)
	// 验证 service.Takedown 被调用且收到正确 siteID。
	assert.Equal(t, 1, sitesSvc.takedownCalls)
	assert.Equal(t, "site-1", sitesSvc.lastSiteID)
}

// TestWebPublishSitesTakedownMapsNotFound 验证 Takedown 站点不存在时返回 404。
func TestWebPublishSitesTakedownMapsNotFound(t *testing.T) {
	sitesSvc := &webPublishSiteServiceStub{takedownErr: service.ErrNotFound}
	router := newWebPublishSitesTestRouter(t, sitesSvc, &webPublishConfigReadServiceStub{})

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/published-sites/no-such-site/disable", nil)
	req = withPrincipal(req, platformAdminPrincipal)
	router.ServeHTTP(recorder, req)

	// 站点不存在应返回 404。
	require.Equal(t, http.StatusNotFound, recorder.Code)
}

// ===== Renew 测试 =====

// TestWebPublishSitesRenewReturns204 验证 Renew 正常路径：service 调用成功时返回 204。
func TestWebPublishSitesRenewReturns204(t *testing.T) {
	sitesSvc := &webPublishSiteServiceStub{}
	router := newWebPublishSitesTestRouter(t, sitesSvc, &webPublishConfigReadServiceStub{})

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/published-sites/site-2/renew", nil)
	req = withPrincipal(req, platformAdminPrincipal)
	router.ServeHTTP(recorder, req)

	// 正常续期返回 204 No Content。
	require.Equal(t, http.StatusNoContent, recorder.Code)
	// 验证 service.Renew 被调用且收到正确 siteID。
	assert.Equal(t, 1, sitesSvc.renewCalls)
	assert.Equal(t, "site-2", sitesSvc.lastSiteID)
}

// TestWebPublishSitesRenewMapsForbidden 验证 Renew 无权限时返回 403。
func TestWebPublishSitesRenewMapsForbidden(t *testing.T) {
	sitesSvc := &webPublishSiteServiceStub{renewErr: service.ErrForbidden}
	router := newWebPublishSitesTestRouter(t, sitesSvc, &webPublishConfigReadServiceStub{})

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/published-sites/site-1/renew", nil)
	req = withPrincipal(req, auth.Principal{UserID: "user-3", Role: domain.UserRoleOrgMember})
	router.ServeHTTP(recorder, req)

	// 跨企业/无权限场景应返回 403。
	require.Equal(t, http.StatusForbidden, recorder.Code)
}

// ===== GetConfig 测试 =====

// TestWebPublishSitesGetConfigReturns200 验证 GetConfig 正常路径：返回 200 + 配置视图。
func TestWebPublishSitesGetConfigReturns200(t *testing.T) {
	configSvc := &webPublishConfigReadServiceStub{
		getResult: service.WebPublishConfigResult{
			OrgID:              "org-1",
			BaseDomain:         "apps.example.com",
			WildcardDomain:     "*.apps.example.com",
			DNSProvider:        "alidns",
			Enabled:            true,
			ProvisioningStatus: "ready",
			CertStatus:         "issued",
		},
	}
	router := newWebPublishSitesTestRouter(t, &webPublishSiteServiceStub{}, configSvc)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/web-publish", nil)
	req = withPrincipal(req, platformAdminPrincipal)
	router.ServeHTTP(recorder, req)

	// 正常路径返回 200。
	require.Equal(t, http.StatusOK, recorder.Code)
	// 验证 service.Get 收到正确 orgID。
	assert.Equal(t, "org-1", configSvc.lastOrgID)
	// 验证响应体包含配置字段。
	var result service.WebPublishConfigResult
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	assert.Equal(t, "org-1", result.OrgID)
	assert.Equal(t, "apps.example.com", result.BaseDomain)
	assert.Equal(t, "issued", result.CertStatus)
}

// TestWebPublishSitesGetConfigMapsNotProvisionedTo403 验证 ErrWebPublishNotProvisioned 被映射为 403，
// 而非 500（Part 1 error-mapping 修复的核心验证场景）。
func TestWebPublishSitesGetConfigMapsNotProvisionedTo403(t *testing.T) {
	configSvc := &webPublishConfigReadServiceStub{getErr: service.ErrWebPublishNotProvisioned}
	router := newWebPublishSitesTestRouter(t, &webPublishSiteServiceStub{}, configSvc)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/web-publish", nil)
	req = withPrincipal(req, platformAdminPrincipal)
	router.ServeHTTP(recorder, req)

	// ErrWebPublishNotProvisioned 必须映射为 403，而非 500。
	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "WEB_PUBLISH_NOT_PROVISIONED")
}

// TestWebPublishSitesGetConfigUnconfiguredReturns200Null 验证企业从未配置 web-publish
// （service 返回 ErrWebPublishNotConfigured）时，GetConfig 返回 200 + null body，而非 500。
// 这是配置页打开未配置企业误报 500「站点管理服务暂时不可用」的回归用例：前端契约
// WebPublishConfigResult | null 依赖该空态返回，500 会让 DNS 下拉与证书面板卡死/报错。
func TestWebPublishSitesGetConfigUnconfiguredReturns200Null(t *testing.T) {
	configSvc := &webPublishConfigReadServiceStub{getErr: service.ErrWebPublishNotConfigured}
	router := newWebPublishSitesTestRouter(t, &webPublishSiteServiceStub{}, configSvc)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/web-publish", nil)
	req = withPrincipal(req, platformAdminPrincipal)
	router.ServeHTTP(recorder, req)

	// 未配置是正常空态：必须 200 而非 500。
	require.Equal(t, http.StatusOK, recorder.Code)
	// body 为 JSON null（前端据此渲染「未配置」初始表单）。
	assert.Equal(t, "null", strings.TrimSpace(recorder.Body.String()))
}

// ===== RetryProvision 测试 =====

// TestWebPublishSitesRetryProvisionReturns204 验证 RetryProvision 正常路径：service 调用成功时返回 204。
func TestWebPublishSitesRetryProvisionReturns204(t *testing.T) {
	configSvc := &webPublishConfigReadServiceStub{}
	router := newWebPublishSitesTestRouter(t, &webPublishSiteServiceStub{}, configSvc)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/platform/organizations/org-1/web-publish/cert/retry", nil)
	req = withPrincipal(req, platformAdminPrincipal)
	router.ServeHTTP(recorder, req)

	// 手动重试成功返回 204 No Content。
	require.Equal(t, http.StatusNoContent, recorder.Code)
	// 验证 service.RetryProvision 被调用且收到正确 orgID。
	assert.Equal(t, 1, configSvc.retryProvisionCalls)
	assert.Equal(t, "org-1", configSvc.lastOrgID)
}

// TestWebPublishSitesRetryProvisionMapsForbidden 验证非平台管理员触发重试时返回 403。
func TestWebPublishSitesRetryProvisionMapsForbidden(t *testing.T) {
	configSvc := &webPublishConfigReadServiceStub{retryErr: service.ErrForbidden}
	router := newWebPublishSitesTestRouter(t, &webPublishSiteServiceStub{}, configSvc)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/platform/organizations/org-1/web-publish/cert/retry", nil)
	req = withPrincipal(req, auth.Principal{UserID: "user-4", Role: domain.UserRoleOrgAdmin})
	router.ServeHTTP(recorder, req)

	// 非平台管理员无权触发手动重试，应返回 403。
	require.Equal(t, http.StatusForbidden, recorder.Code)
}

// ===== ErrWebPublishNotProvisioned 映射单元测试 =====

// TestServiceErrorMappingWebPublishNotProvisionedMaps403 直接通过 writeMappedServiceError
// 验证 ErrWebPublishNotProvisioned 在 mappedServiceErrorRules 中的规则映射到 403，
// 与 handler 无关，聚焦 request_errors.go 映射表本身的正确性。
func TestServiceErrorMappingWebPublishNotProvisionedMaps403(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	// 直接调用 writeMappedServiceError，验证 ErrWebPublishNotProvisioned → 403。
	writeMappedServiceError(c, service.ErrWebPublishNotProvisioned, http.StatusInternalServerError, apierror.MsgInternal)

	// 必须命中 403，而非兜底的 500。
	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "WEB_PUBLISH_NOT_PROVISIONED")
}
