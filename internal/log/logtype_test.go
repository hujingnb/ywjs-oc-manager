package log

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseLastLogType 解析 buf 中最后一条 JSON 日志为 map，供断言 log_type 字段。
func parseLastLogType(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	require.NotEmpty(t, lines)
	var m map[string]any
	require.NoError(t, json.Unmarshal(lines[len(lines)-1], &m))
	return m
}

// TestLogType_未带时兜底注入app 覆盖业务普通日志场景：未显式带 log_type 时
// 由 requestIDHandler 兜底注入 app，使其也能按类型过滤。
func TestLogType_未带时兜底注入app(t *testing.T) {
	var buf bytes.Buffer
	logger := NewSlogLogger(&buf, Config{Level: slog.LevelInfo, Format: "json"})
	logger.InfoContext(context.Background(), "业务里程碑")
	m := parseLastLogType(t, &buf)
	assert.Equal(t, LogTypeApp, m[KeyLogType]) // 未带 → 兜底 app
}

// TestLogType_已带时不被覆盖 覆盖基础设施日志场景：调用点已显式带 log_type=http
// 时，兜底逻辑不得改写，避免把 access log 误标成 app。
func TestLogType_已带时不被覆盖(t *testing.T) {
	var buf bytes.Buffer
	logger := NewSlogLogger(&buf, Config{Level: slog.LevelInfo, Format: "json"})
	logger.LogAttrs(context.Background(), slog.LevelInfo, "http_request", slog.String(KeyLogType, LogTypeHTTP))
	m := parseLastLogType(t, &buf)
	assert.Equal(t, LogTypeHTTP, m[KeyLogType]) // 已带 → 保持 http，不被覆盖
}

// TestLogType_text格式同样兜底 覆盖 text 输出格式：兜底注入与脱敏、trace_id 一样
// 不受 handler 输出格式影响。
func TestLogType_text格式同样兜底(t *testing.T) {
	var buf bytes.Buffer
	logger := NewSlogLogger(&buf, Config{Level: slog.LevelInfo, Format: "text"})
	logger.InfoContext(context.Background(), "业务里程碑")
	assert.Contains(t, buf.String(), "log_type=app") // text 格式下也兜底
}
