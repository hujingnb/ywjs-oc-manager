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
			assert.Equal(t, "http", m["log_type"]) // 每条 access log 带 log_type=http
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

// TestAccessLog_鉴权前挂载仍记user_id 复刻生产挂载顺序：AccessLog 先入栈、
// RequireUserAuth 后入栈并在 c.Next() 阶段注入 principal，验证 AccessLog 收尾时
// 仍能从 c.Request.Context() 读到 user_id（中间件先进后出 + 替换 c.Request 指针）。
func TestAccessLog_鉴权前挂载仍记user_id(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(newCapturingLogger(&buf))
	defer slog.SetDefault(old)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	// AccessLog 在鉴权之前挂载（生产顺序），先于 auth-sim 入栈。
	r.Use(AccessLog())
	// 模拟 RequireUserAuth：在 c.Next() 之前替换 c.Request 注入 principal。
	r.Use(func(c *gin.Context) {
		ctx := auth.WithPrincipal(c.Request.Context(), auth.Principal{UserID: "u-789"})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ping", nil))

	m := lastLogLine(t, &buf)
	assert.Equal(t, "u-789", m["user_id"]) // 鉴权前挂载仍能拿到 principal
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
