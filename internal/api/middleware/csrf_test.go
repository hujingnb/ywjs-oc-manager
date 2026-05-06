package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newCSRFRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequireCSRF())
	r.POST("/api/v1/things", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/api/v1/things", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.POST("/api/v1/agent/heartbeat", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.POST("/api/v1/auth/login", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func TestCSRF_AllowsSafeMethods(t *testing.T) {
	r := newCSRFRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/things", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET should bypass CSRF; code=%d", w.Code)
	}
}

func TestCSRF_AllowsRequestWithoutCookieOptIn(t *testing.T) {
	r := newCSRFRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/things", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("无 CSRF cookie 应 opt-in 放行; code=%d", w.Code)
	}
}

func TestCSRF_RejectsCookiePresentButHeaderMissing(t *testing.T) {
	r := newCSRFRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/things", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "abc"})
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("有 cookie 但缺 header 应 403; code=%d", w.Code)
	}
}

func TestCSRF_RejectsHeaderMismatch(t *testing.T) {
	r := newCSRFRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/things", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "abc"})
	req.Header.Set(CSRFHeaderName, "xyz")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("header 不等于 cookie 应 403; code=%d", w.Code)
	}
}

func TestCSRF_AcceptsHeaderMatch(t *testing.T) {
	r := newCSRFRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/things", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "abc"})
	req.Header.Set(CSRFHeaderName, "abc")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("header 等于 cookie 应通过; code=%d", w.Code)
	}
}

func TestCSRF_ExemptsAgentPath(t *testing.T) {
	r := newCSRFRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/heartbeat", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "abc"})
	// 故意不带 header；agent 路径应跳过校验。
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("agent 路径应跳过 CSRF; code=%d", w.Code)
	}
}

func TestCSRF_ExemptsLoginPath(t *testing.T) {
	r := newCSRFRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("登录路径应跳过 CSRF; code=%d", w.Code)
	}
}
