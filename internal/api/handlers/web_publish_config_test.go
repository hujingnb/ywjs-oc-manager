// Package handlers 的 web_publish_config_test.go 覆盖企业 web-publish 配置 handler 的主要场景。
package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// webPublishConfigServiceStub 是 webPublishConfigService 的测试 stub，记录调用参数与可配错误。
type webPublishConfigServiceStub struct {
	// configureCalls 记录 Configure 被调用的次数。
	configureCalls int
	// lastConfigureInput 记录最后一次 Configure 调用的 service 入参。
	lastConfigureInput service.WebPublishConfigInput
	// configureErr 是 Configure 返回的预设错误，nil 表示成功。
	configureErr error

	// enableCalls 记录 Enable 被调用的次数。
	enableCalls int
	// lastEnableOrgID 记录最后一次 Enable 调用的企业 ID。
	lastEnableOrgID string
	// enableErr 是 Enable 返回的预设错误，nil 表示成功。
	enableErr error

	// disableCalls 记录 Disable 被调用的次数。
	disableCalls int
	// lastDisableOrgID 记录最后一次 Disable 调用的企业 ID。
	lastDisableOrgID string
	// disableErr 是 Disable 返回的预设错误，nil 表示成功。
	disableErr error
}

func (s *webPublishConfigServiceStub) Configure(_ context.Context, _ auth.Principal, in service.WebPublishConfigInput) error {
	s.configureCalls++
	s.lastConfigureInput = in
	return s.configureErr
}

func (s *webPublishConfigServiceStub) Enable(_ context.Context, _ auth.Principal, orgID string) error {
	s.enableCalls++
	s.lastEnableOrgID = orgID
	return s.enableErr
}

func (s *webPublishConfigServiceStub) Disable(_ context.Context, _ auth.Principal, orgID string) error {
	s.disableCalls++
	s.lastDisableOrgID = orgID
	return s.disableErr
}

// newWebPublishConfigTestRouter 创建绑定了 WebPublishConfigHandler 的测试 gin 路由。
func newWebPublishConfigTestRouter(t *testing.T, svc webPublishConfigService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterWebPublishConfigRoutes(router, NewWebPublishConfigHandler(svc))
	return router
}

// TestWebPublishConfigureMissingBaseDomain 验证 Configure 接口在缺少 base_domain 必填字段时返回 400，且不调用 service。
func TestWebPublishConfigureMissingBaseDomain(t *testing.T) {
	// 缺少必填字段 base_domain，handler 应在绑定阶段直接返回 400，不透传 service。
	stub := &webPublishConfigServiceStub{}
	router := newWebPublishConfigTestRouter(t, stub)

	recorder := httptest.NewRecorder()
	// 请求体仅携带 dns_provider，缺少 base_domain。
	request := httptest.NewRequest(http.MethodPut, "/api/v1/platform/organizations/org-1/web-publish",
		bytes.NewBufferString(`{"dns_provider":"aliyun"}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	// 缺少必填字段应返回 400。
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	// 响应应包含 BAD_REQUEST code，方便前端程序化处理。
	assert.Contains(t, recorder.Body.String(), "BAD_REQUEST")
	// 不应触达 service。
	assert.Equal(t, 0, stub.configureCalls)
}

// TestWebPublishConfigureMissingDNSProvider 验证 Configure 接口在缺少 dns_provider 必填字段时返回 400。
func TestWebPublishConfigureMissingDNSProvider(t *testing.T) {
	// 缺少必填字段 dns_provider，handler 应在绑定阶段直接返回 400。
	stub := &webPublishConfigServiceStub{}
	router := newWebPublishConfigTestRouter(t, stub)

	recorder := httptest.NewRecorder()
	// 请求体仅携带 base_domain，缺少 dns_provider。
	request := httptest.NewRequest(http.MethodPut, "/api/v1/platform/organizations/org-1/web-publish",
		bytes.NewBufferString(`{"base_domain":"apps.example.com"}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	// 缺少必填字段应返回 400。
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Equal(t, 0, stub.configureCalls)
}

// TestWebPublishConfigureValidBodyCallsServiceAndReturns204 验证 Configure 接口在合法请求体时调用 service 并返回 204。
func TestWebPublishConfigureValidBodyCallsServiceAndReturns204(t *testing.T) {
	// 合法请求体，验证 handler 正确调用 service 并返回 204。
	stub := &webPublishConfigServiceStub{}
	router := newWebPublishConfigTestRouter(t, stub)

	recorder := httptest.NewRecorder()
	body := `{"base_domain":"apps.example.com","dns_provider":"aliyun","credentials":{"access_key_id":"ak","access_key_secret":"sk"},"site_ttl_days":14,"max_sites":50}`
	request := httptest.NewRequest(http.MethodPut, "/api/v1/platform/organizations/org-99/web-publish",
		bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	// 成功路径应返回 204 No Content。
	require.Equal(t, http.StatusNoContent, recorder.Code)
	// service 应被调用恰好一次。
	assert.Equal(t, 1, stub.configureCalls)
	// OrgID 应从路径参数正确提取。
	assert.Equal(t, "org-99", stub.lastConfigureInput.OrgID)
	// 字段值应被正确透传。
	assert.Equal(t, "apps.example.com", stub.lastConfigureInput.BaseDomain)
	assert.Equal(t, "aliyun", stub.lastConfigureInput.DNSProvider)
	assert.Equal(t, "ak", stub.lastConfigureInput.Credentials["access_key_id"])
	assert.Equal(t, "sk", stub.lastConfigureInput.Credentials["access_key_secret"])
	// int32 → int 转换应正确。
	assert.Equal(t, 14, stub.lastConfigureInput.SiteTTLDays)
	assert.Equal(t, 50, stub.lastConfigureInput.MaxSites)
}

// TestWebPublishConfigureServiceErrorMaps403 验证 Configure 接口在 service 返回 ErrForbidden 时映射为 403。
func TestWebPublishConfigureServiceErrorMaps403(t *testing.T) {
	// service 返回 ErrForbidden，验证 handler 正确映射为 403。
	stub := &webPublishConfigServiceStub{configureErr: service.ErrForbidden}
	router := newWebPublishConfigTestRouter(t, stub)

	recorder := httptest.NewRecorder()
	body := `{"base_domain":"apps.example.com","dns_provider":"aliyun"}`
	request := httptest.NewRequest(http.MethodPut, "/api/v1/platform/organizations/org-1/web-publish",
		bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	// service 权限拒绝应映射为 403。
	require.Equal(t, http.StatusForbidden, recorder.Code)
}

// TestWebPublishEnableCallsServiceAndReturns204 验证 Enable 接口调用 service.Enable 并返回 204。
func TestWebPublishEnableCallsServiceAndReturns204(t *testing.T) {
	// Enable 正常路径：验证 handler 调用 service 并返回 204。
	stub := &webPublishConfigServiceStub{}
	router := newWebPublishConfigTestRouter(t, stub)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/platform/organizations/org-42/web-publish/enable", nil)
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	// 开通成功应返回 204 No Content。
	require.Equal(t, http.StatusNoContent, recorder.Code)
	// service.Enable 应被调用一次，且企业 ID 与路径参数一致。
	assert.Equal(t, 1, stub.enableCalls)
	assert.Equal(t, "org-42", stub.lastEnableOrgID)
}

// TestWebPublishEnableServiceErrorMaps403 验证 Enable 接口在 service 返回 ErrForbidden 时映射为 403。
func TestWebPublishEnableServiceErrorMaps403(t *testing.T) {
	// service 权限拒绝，验证 handler 正确映射为 403。
	stub := &webPublishConfigServiceStub{enableErr: service.ErrForbidden}
	router := newWebPublishConfigTestRouter(t, stub)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/platform/organizations/org-1/web-publish/enable", nil)
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
}

// TestWebPublishDisableCallsServiceAndReturns204 验证 Disable 接口调用 service.Disable 并返回 204。
func TestWebPublishDisableCallsServiceAndReturns204(t *testing.T) {
	// Disable 正常路径：验证 handler 调用 service 并返回 204。
	stub := &webPublishConfigServiceStub{}
	router := newWebPublishConfigTestRouter(t, stub)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/platform/organizations/org-7/web-publish/disable", nil)
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	// 停用成功应返回 204 No Content。
	require.Equal(t, http.StatusNoContent, recorder.Code)
	// service.Disable 应被调用一次，且企业 ID 与路径参数一致。
	assert.Equal(t, 1, stub.disableCalls)
	assert.Equal(t, "org-7", stub.lastDisableOrgID)
}
