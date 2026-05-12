// Package log 的 safe_error_test 覆盖对外错误消息的令牌、路径和空值脱敏。
package log

import (
	"errors"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

// TestSafeErrorMessage_Nil 验证安全错误当前用户接口ssage空值的边界条件场景。
func TestSafeErrorMessage_Nil(t *testing.T) {
	require.Empty(t, SafeErrorMessage(nil))
}

// TestSafeErrorMessage_RedactsToken 验证安全错误当前用户接口ssage脱敏令牌的预期行为场景。
func TestSafeErrorMessage_RedactsToken(t *testing.T) {
	got := SafeErrorMessage(errors.New(`failed: Bearer eyJabc123 is invalid`))
	require.NotContains(t, got, "eyJabc123")
	require.Contains(t, got, "Bearer ***")
}

// TestSafeErrorMessage_StripsFilePath 验证安全错误当前用户接口ssage移除文件路径的预期行为场景。
func TestSafeErrorMessage_StripsFilePath(t *testing.T) {
	got := SafeErrorMessage(errors.New(`scan: /home/hujing/oc-manager/internal/service/foo.go:42 unexpected EOF`))
	require.NotContains(t, got, "/home/hujing/")
	require.Contains(t, got, "<path>")
}

// TestSafeErrorMessage_StripsSQL 验证安全错误当前用户接口ssage移除SQL的预期行为场景。
func TestSafeErrorMessage_StripsSQL(t *testing.T) {
	got := SafeErrorMessage(errors.New(`pq: SELECT id, password_hash FROM users WHERE username='admin';`))
	require.NotContains(t, got, "password_hash")
	require.Contains(t, got, "<sql>")
}

// TestSafeErrorMessage_Truncates 验证安全错误当前用户接口ssage截断的预期行为场景。
func TestSafeErrorMessage_Truncates(t *testing.T) {
	long := strings.Repeat("x", 500)
	got := SafeErrorMessage(errors.New(long))
	if len(got) > safeMsgMax+5 {
		t.Fatalf("got len = %d, 应截到 %d 加省略号", len(got), safeMsgMax)
	}
	require.True(t, strings.HasSuffix(got, "..."))
}
