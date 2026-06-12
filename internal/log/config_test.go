package log

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestParseLevel 覆盖 logging.level 各取值与非法值 fallback。
func TestParseLevel(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want slog.Level
	}{
		{name: "debug 小写", in: "debug", want: slog.LevelDebug},          // 正常：debug
		{name: "INFO 大写", in: "INFO", want: slog.LevelInfo},              // 正常：大小写不敏感
		{name: "warn", in: "warn", want: slog.LevelWarn},                  // 正常：warn
		{name: "warning 别名", in: "warning", want: slog.LevelWarn},         // 正常：warning 作为 warn 别名
		{name: "error 带空格", in: " error ", want: slog.LevelError},        // 边界：首尾空格应被裁剪
		{name: "非法值 fallback Info", in: "verbose", want: slog.LevelInfo}, // 异常：非法值回退 Info
		{name: "空串 fallback Info", in: "", want: slog.LevelInfo},          // 边界：空串回退 Info
	}
	for _, tc := range cases {
		// 子测试覆盖该 level 输入对应的解析结果。
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, parseLevel(tc.in))
		})
	}
}

// TestParseFormat 覆盖 logging.format 取值与非法值 fallback。
func TestParseFormat(t *testing.T) {
	assert.Equal(t, "text", parseFormat("text")) // 正常：text
	assert.Equal(t, "text", parseFormat("TEXT")) // 正常：大小写不敏感
	assert.Equal(t, "json", parseFormat("json")) // 正常：json
	assert.Equal(t, "json", parseFormat("xml"))  // 异常：非法值回退 json
	assert.Equal(t, "json", parseFormat(""))     // 边界：空串回退 json
}

// TestNewSlogLogger_text格式仍脱敏 验证 text 格式下脱敏仍生效。
func TestNewSlogLogger_text格式仍脱敏(t *testing.T) {
	var buf bytes.Buffer
	logger := NewSlogLogger(&buf, Config{Level: slog.LevelInfo, Format: "text"})
	logger.Info("login", slog.String("password", "hunter2"))
	out := buf.String()
	assert.NotContains(t, out, "hunter2") // 脱敏：password 值不应出现
	assert.Contains(t, out, "***")        // 脱敏：替换为 ***
}

// TestNewSlogLogger_级别过滤 验证低于配置级别的记录被丢弃。
func TestNewSlogLogger_级别过滤(t *testing.T) {
	var buf bytes.Buffer
	logger := NewSlogLogger(&buf, Config{Level: slog.LevelInfo, Format: "json"})
	logger.Debug("noisy", slog.String("k", "v")) // Debug 低于 Info，应被丢弃
	assert.Empty(t, buf.String())                // 无输出
}
