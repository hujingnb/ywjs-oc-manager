package httplog

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rtFunc 把函数适配为 http.RoundTripper，用作测试 base transport。
type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// resp 构造一个指定状态码的最小响应。
func resp(code int) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}
}

// capture 返回写入指定 buffer、最低记 Debug 的 JSON logger，便于断言日志字段。
func capture(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// lastLine 解析 buffer 中最后一行 JSON 日志为 map。
func lastLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	require.NotEmpty(t, lines)
	var m map[string]any
	require.NoError(t, json.Unmarshal(lines[len(lines)-1], &m))
	return m
}

// TestRoundTrip_成功记debug 验证 2xx 记 Debug 且字段齐全、endpoint 不含 query、query 中的 secret 不进日志。
func TestRoundTrip_成功记debug(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(capture(&buf))
	defer slog.SetDefault(old)

	base := rtFunc(func(r *http.Request) (*http.Response, error) { return resp(200), nil })
	rt := New(base, "newapi")
	req, _ := http.NewRequest(http.MethodGet, "http://x/api/user?token=secret", nil)
	_, err := rt.RoundTrip(req)
	require.NoError(t, err)

	m := lastLine(t, &buf)
	assert.Equal(t, "external_request", m["msg"])
	assert.Equal(t, "DEBUG", m["level"])
	assert.Equal(t, "newapi", m["log_type"]) // 用 log_type 区分外部依赖，取代独立 service 字段
	assert.Equal(t, "GET", m["method"])
	assert.Equal(t, "/api/user", m["endpoint"]) // 不含 query
	assert.Equal(t, float64(200), m["status"])
	assert.NotContains(t, buf.String(), "secret") // query 不进日志
}

// TestRoundTrip_非2xx记warn 验证 4xx/5xx 记 Warn 并带 status。
func TestRoundTrip_非2xx记warn(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(capture(&buf))
	defer slog.SetDefault(old)

	base := rtFunc(func(r *http.Request) (*http.Response, error) { return resp(500), nil })
	req, _ := http.NewRequest(http.MethodGet, "http://x/api/user", nil)
	_, _ = New(base, "ragflow").RoundTrip(req)

	m := lastLine(t, &buf)
	assert.Equal(t, "WARN", m["level"])
	assert.Equal(t, float64(500), m["status"])
}

// TestRoundTrip_3xx记debug 验证 3xx（如 304 条件命中 / 重定向）不是错误，记 Debug 而非 Warn，避免噪音告警。
func TestRoundTrip_3xx记debug(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(capture(&buf))
	defer slog.SetDefault(old)

	base := rtFunc(func(r *http.Request) (*http.Response, error) { return resp(304), nil }) // 304 Not Modified
	req, _ := http.NewRequest(http.MethodGet, "http://x/api/user", nil)
	_, _ = New(base, "newapi").RoundTrip(req)

	m := lastLine(t, &buf)
	assert.Equal(t, "DEBUG", m["level"])     // 3xx 归 Debug
	assert.Equal(t, float64(304), m["status"]) // 状态码仍记录
}

// TestRoundTrip_传输错误记warn 验证 transport error 记 Warn、带 error 不带 status，且错误原样透传。
func TestRoundTrip_传输错误记warn(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(capture(&buf))
	defer slog.SetDefault(old)

	base := rtFunc(func(r *http.Request) (*http.Response, error) { return nil, errors.New("dial fail") })
	req, _ := http.NewRequest(http.MethodGet, "http://x/api/user", nil)
	_, err := New(base, "newapi").RoundTrip(req)
	require.Error(t, err) // 错误必须原样透传给调用方

	m := lastLine(t, &buf)
	assert.Equal(t, "WARN", m["level"])
	assert.Equal(t, "dial fail", m["error"])
	_, hasStatus := m["status"]
	assert.False(t, hasStatus) // transport error 无状态码
}
