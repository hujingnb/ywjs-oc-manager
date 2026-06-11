package log

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestErr 验证 Err helper 用统一 key "error" 包装错误信息。
func TestErr(t *testing.T) {
	attr := Err(errors.New("boom"))
	assert.Equal(t, KeyError, attr.Key)         // 统一 key 常量
	assert.Equal(t, "boom", attr.Value.String()) // 值为 err.Error()
}

// TestErr_nil 验证 nil error 返回空串值，避免 panic。
func TestErr_nil(t *testing.T) {
	attr := Err(nil)
	assert.Equal(t, KeyError, attr.Key) // key 仍为 error
	assert.Equal(t, "", attr.Value.String()) // 值为空串
}
