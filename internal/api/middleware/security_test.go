package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

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

func TestMaskSecret_ReplacesMatchedFields(t *testing.T) {
	input := `authorization=Bearer xyz, agent_token=abc, master_key="secret-key", username=alice`
	got := MaskSecret(input)
	for _, fragment := range []string{"xyz", "abc", "secret-key"} {
		require.NotContains(t, got, fragment)
	}
	require.Contains(t, got, "alice")
	require.Contains(t, got, "***")
}
