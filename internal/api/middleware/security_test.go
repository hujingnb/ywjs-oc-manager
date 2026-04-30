package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
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
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatalf("ACAO header = %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
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
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("非白名单不应回写 ACAO，实际 %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
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
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", rec.Code)
	}
}

func TestMaskSecret_ReplacesMatchedFields(t *testing.T) {
	input := `authorization=Bearer xyz, agent_token=abc, master_key="secret-key", username=alice`
	got := MaskSecret(input)
	for _, fragment := range []string{"xyz", "abc", "secret-key"} {
		if strings.Contains(got, fragment) {
			t.Fatalf("敏感片段 %q 未被脱敏: %s", fragment, got)
		}
	}
	if !strings.Contains(got, "alice") {
		t.Fatalf("非敏感字段被误改: %s", got)
	}
	if !strings.Contains(got, "***") {
		t.Fatalf("缺少 *** 替换: %s", got)
	}
}
