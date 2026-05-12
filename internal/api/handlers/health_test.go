// Package handlers 的 health_test 覆盖健康检查接口的响应结构和状态码约定。
package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestHealthRouteReturnsStablePayload 验证健康检查路由返回稳定载荷的成功路径场景。
func TestHealthRouteReturnsStablePayload(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterHealthRoutes(router)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)

	var response HealthResponse
	err := json.Unmarshal(recorder.Body.Bytes(), &response)
	require.NoError(t, err)
	require.Equal(t, "ok", response.Status)
	require.NotEqual(t, "", response.Time)
}
