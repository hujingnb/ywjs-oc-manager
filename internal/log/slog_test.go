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

// captureLogger 用 bytes.Buffer 捕获日志输出便于断言。
func captureLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	logger := NewSlogLogger(buf)
	return logger, buf
}

func TestNewSlogLogger_输出合法JSON并含核心字段(t *testing.T) {
	logger, buf := captureLogger()
	logger.Info("hello", "user_id", "u-1")
	var got map[string]any
	err := json.Unmarshal(buf.Bytes(), &got)
	require.NoError(t, err)
	assert.Equal(t, "hello", got["msg"])
	assert.Equal(t, "INFO", got["level"])
	assert.Equal(t, "u-1", got["user_id"])
	if _, ok := got["time"]; !ok {
		t.Errorf("missing time field: %v", got)
	}
	if _, ok := got["source"]; !ok {
		t.Errorf("missing source field（AddSource=true 应输出 source）: %v", got)
	}
}

func TestNewSlogLogger_redact生效(t *testing.T) {
	logger, buf := captureLogger()
	// 写入会被 redactlog 命中的字段
	logger.Info("api call", "api_key", "sk-secret-12345abcde")
	out := buf.String()
	assert.NotContains(t, out, "sk-secret-12345abcde")
}

func TestRequestIDExtractor_默认为空串(t *testing.T) {
	logger, buf := captureLogger()
	ctx := context.Background()
	logger.InfoContext(ctx, "no trace")
	out := buf.String()
	assert.NotContains(t, out, "trace_id")
}

type ctxTestKey string

func TestSetRequestIDExtractor_注入trace_id(t *testing.T) {
	original := requestIDExtractor
	t.Cleanup(func() { requestIDExtractor = original })

	const key ctxTestKey = "test-trace"
	SetRequestIDExtractor(func(ctx context.Context) string {
		if v, ok := ctx.Value(key).(string); ok {
			return v
		}
		return ""
	})

	logger, buf := captureLogger()
	ctx := context.WithValue(context.Background(), key, "abc123")
	logger.InfoContext(ctx, "with trace")

	var got map[string]any
	err := json.Unmarshal(buf.Bytes(), &got)
	require.NoError(t, err)
	assert.Equal(t, "abc123", got["trace_id"])
}

func TestSetRequestIDExtractor_空串不写入字段(t *testing.T) {
	original := requestIDExtractor
	t.Cleanup(func() { requestIDExtractor = original })

	SetRequestIDExtractor(func(ctx context.Context) string { return "" })

	logger, buf := captureLogger()
	logger.InfoContext(context.Background(), "no trace")

	var got map[string]any
	err := json.Unmarshal(buf.Bytes(), &got)
	require.NoError(t, err)
	if _, ok := got["trace_id"]; ok {
		t.Errorf("不应写入空 trace_id 字段，got %v", got)
	}
}
