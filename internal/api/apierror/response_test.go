// Package apierror 的单元测试：覆盖 ErrorResponse 构造器与 JSON 序列化。
//
// 测试目标只关心对外字段名、字段值与 JSON 形态，不应依赖 gin 或 handler 实现细节。
package apierror

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNew_FieldsMatch 覆盖正常路径：传入的 code 与 message 必须原样落到结构体字段。
// 用于防止后续误调换参数顺序或字段名重命名。
func TestNew_FieldsMatch(t *testing.T) {
	resp := New("APP_NOT_FOUND", "应用不存在")
	assert.Equal(t, "APP_NOT_FOUND", resp.Code)
	assert.Equal(t, "应用不存在", resp.Message)
}

// TestNew_Marshal 覆盖 JSON 序列化形态：字段必须为 "code" 与 "message"，
// 顺序由 encoding/json 保证按结构体字段声明顺序输出，前端类型生成依赖此契约。
func TestNew_Marshal(t *testing.T) {
	resp := New("NO_NODE_AVAILABLE", "暂无可用 Runtime Node")
	raw, err := json.Marshal(resp)
	require.NoError(t, err)
	assert.JSONEq(t, `{"code":"NO_NODE_AVAILABLE","message":"暂无可用 Runtime Node"}`, string(raw))
}

// TestErrorResponse_ZeroValue 覆盖零值边界：调用方未通过 New 而直接声明
// ErrorResponse{} 时，序列化结果应为空字符串字段而非 omitempty 隐藏。
// 前端类型契约中 code 与 message 都是 required，零值也必须显式出现。
func TestErrorResponse_ZeroValue(t *testing.T) {
	raw, err := json.Marshal(ErrorResponse{})
	require.NoError(t, err)
	assert.JSONEq(t, `{"code":"","message":""}`, string(raw))
}
