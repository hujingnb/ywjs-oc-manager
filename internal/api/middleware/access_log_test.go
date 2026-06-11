package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
)

// newCapturingLogger 返回写入 buf 的 JSON logger，供断言日志字段。
func newCapturingLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// lastLogLine 解析 buf 中最后一条 JSON 日志为 map。
func lastLogLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	require.NotEmpty(t, lines)
	var m map[string]any
	require.NoError(t, json.Unmarshal(lines[len(lines)-1], &m))
	return m
}

// TestAccessLog_级别按状态分流 验证 2xx→info、4xx→warn、5xx→error。
func TestAccessLog_级别按状态分流(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		wantLevel string
	}{
		{name: "2xx info", status: http.StatusOK, wantLevel: "INFO"},                     // 正常请求记 Info
		{name: "4xx warn", status: http.StatusBadRequest, wantLevel: "WARN"},             // 客户端错误记 Warn
		{name: "5xx error", status: http.StatusInternalServerError, wantLevel: "ERROR"}, // 服务端错误记 Error
	}
	for _, tc := range cases {
		// 子测试覆盖该状态码对应的日志级别。
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			old := slog.Default()
			slog.SetDefault(newCapturingLogger(&buf))
			defer slog.SetDefault(old)

			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.Use(AccessLog())
			r.GET("/api/v1/orgs/:id", func(c *gin.Context) { c.Status(tc.status) })

			req := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/abc", nil)
			r.ServeHTTP(httptest.NewRecorder(), req)

			m := lastLogLine(t, &buf)
			assert.Equal(t, "http_request", m["msg"])
			assert.Equal(t, tc.wantLevel, m["level"])
			assert.Equal(t, "/api/v1/orgs/:id", m["route"]) // route 用模板而非真实 ID
			assert.Equal(t, "GET", m["method"])
			assert.Equal(t, float64(tc.status), m["status"])
		})
	}
}

// TestAccessLog_带user_id 验证 ctx 中有 principal 时记录 user_id。
func TestAccessLog_带user_id(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(newCapturingLogger(&buf))
	defer slog.SetDefault(old)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	// 模拟 auth 中间件：把 principal 注入 request ctx。
	r.Use(func(c *gin.Context) {
		ctx := auth.WithPrincipal(c.Request.Context(), auth.Principal{UserID: "u-123"})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.Use(AccessLog())
	r.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ping", nil))

	m := lastLogLine(t, &buf)
	assert.Equal(t, "u-123", m["user_id"])
}

// TestAccessLog_跳过健康检查 验证 /healthz 不产生 access log。
func TestAccessLog_跳过健康检查(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(newCapturingLogger(&buf))
	defer slog.SetDefault(old)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AccessLog())
	r.GET("/healthz", func(c *gin.Context) { c.Status(http.StatusOK) })

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	assert.Empty(t, buf.String()) // 健康检查不记日志
}
