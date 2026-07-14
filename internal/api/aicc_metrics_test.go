package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// TestAICCDispatchMetricsRouteRequiresPlatformAdmin 覆盖指标端点：
// 只有平台管理员携带有效 JWT 才能读取跨企业异步客服指标快照。
func TestAICCDispatchMetricsRouteRequiresPlatformAdmin(t *testing.T) {
	manager, err := auth.NewTokenManager("access-secret", "refresh-secret", time.Hour, time.Hour)
	require.NoError(t, err)
	observer := service.NewSlogAICCDispatchObserver(nil)
	router := NewRouter(Dependencies{TokenManager: manager, AICCDispatchMetrics: observer})

	// 企业管理员无权读取跨企业聚合指标。
	orgToken, err := manager.SignAccessToken(auth.Principal{UserID: "org-user", OrgID: "org", Role: "org_admin"})
	require.NoError(t, err)
	orgRequest := httptest.NewRequest(http.MethodGet, "/api/v1/platform/aicc/metrics", nil)
	orgRequest.Header.Set("Authorization", "Bearer "+orgToken)
	orgRecorder := httptest.NewRecorder()
	router.ServeHTTP(orgRecorder, orgRequest)
	assert.Equal(t, http.StatusForbidden, orgRecorder.Code)

	// 平台管理员读取到稳定 JSON 指标快照。
	platformToken, err := manager.SignAccessToken(auth.Principal{UserID: "platform-user", Role: "platform_admin"})
	require.NoError(t, err)
	platformRequest := httptest.NewRequest(http.MethodGet, "/api/v1/platform/aicc/metrics", nil)
	platformRequest.Header.Set("Authorization", "Bearer "+platformToken)
	platformRecorder := httptest.NewRecorder()
	router.ServeHTTP(platformRecorder, platformRequest)
	assert.Equal(t, http.StatusOK, platformRecorder.Code)
	assert.Contains(t, platformRecorder.Body.String(), "counters")
	// app_id 是 HPA External 指标 selector 的唯一隔离键，受控桥接层据此导出带标签的 gauge。
	assert.Contains(t, platformRecorder.Body.String(), "queue_depth_by_app")
	assert.Contains(t, platformRecorder.Body.String(), "inflight_by_app")
}
