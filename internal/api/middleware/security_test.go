// Package middleware 的 security_test 覆盖 CORS 与安全响应头 middleware 的边界行为。
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestCORSAllowOrigin_AllowsListedOrigin 验证CORS allow origin允许白名单 origin的预期行为场景。
func TestCORSAllowOrigin_AllowsListedOrigin(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(CORSAllowOrigin([]string{"https://app.example.com"}))
	router.GET("/x", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, "https://app.example.com", rec.Header().Get("Access-Control-Allow-Origin"))
}

// TestCORSAllowOrigin_RejectsUnlisted 验证CORS allow origin拒绝非白名单 origin的异常或拒绝路径场景。
func TestCORSAllowOrigin_RejectsUnlisted(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(CORSAllowOrigin([]string{"https://app.example.com"}))
	router.GET("/x", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://evil.com")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, "", rec.Header().Get("Access-Control-Allow-Origin"))
}

// TestCORSAllowOrigin_HandlesPreflight 验证CORS allow origin处理Preflight的预期行为场景。
func TestCORSAllowOrigin_HandlesPreflight(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(CORSAllowOrigin([]string{"https://app.example.com"}))
	router.GET("/x", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodOptions, "/x", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
}

// TestMaskSecret_ReplacesMatchedFields 验证掩码密钥替换s匹配ed字段的预期行为场景。
func TestMaskSecret_ReplacesMatchedFields(t *testing.T) {
	input := `authorization=Bearer xyz, agent_token=abc, master_key="secret-key", username=alice`
	got := MaskSecret(input)
	for _, fragment := range []string{"xyz", "abc", "secret-key"} {
		require.NotContains(t, got, fragment)
	}
	require.Contains(t, got, "alice")
	require.Contains(t, got, "***")
}
