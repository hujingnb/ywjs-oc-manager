package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
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

// TestCSRF_AllowsSafeMethods 验证CSRF允许安全方法的预期行为场景。
func TestCSRF_AllowsSafeMethods(t *testing.T) {
	r := newCSRFRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/things", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

// TestCSRF_AllowsRequestWithoutCookieOptIn 验证CSRF允许请求未启用 cookie opt-in的预期行为场景。
func TestCSRF_AllowsRequestWithoutCookieOptIn(t *testing.T) {
	r := newCSRFRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/things", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

// TestCSRF_RejectsCookiePresentButHeaderMissing 验证CSRF拒绝cookie 存在但请求头缺失的异常或拒绝路径场景。
func TestCSRF_RejectsCookiePresentButHeaderMissing(t *testing.T) {
	r := newCSRFRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/things", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "abc"})
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)
}

// TestCSRF_RejectsHeaderMismatch 验证CSRF拒绝请求头不匹配的异常或拒绝路径场景。
func TestCSRF_RejectsHeaderMismatch(t *testing.T) {
	r := newCSRFRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/things", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "abc"})
	req.Header.Set(CSRFHeaderName, "xyz")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)
}

// TestCSRF_AcceptsHeaderMatch 验证CSRF接受请求头匹配的预期行为场景。
func TestCSRF_AcceptsHeaderMatch(t *testing.T) {
	r := newCSRFRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/things", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "abc"})
	req.Header.Set(CSRFHeaderName, "abc")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

// TestCSRF_ExemptsAgentPath 验证CSRFExemptsagent路径的预期行为场景。
func TestCSRF_ExemptsAgentPath(t *testing.T) {
	r := newCSRFRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/heartbeat", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "abc"})
	// 故意不带 header；agent 路径应跳过校验。
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

// TestCSRF_ExemptsLoginPath 验证CSRFExempts登录路径的预期行为场景。
func TestCSRF_ExemptsLoginPath(t *testing.T) {
	r := newCSRFRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}
