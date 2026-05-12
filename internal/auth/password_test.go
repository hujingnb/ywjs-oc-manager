// Package auth 的测试覆盖密码、令牌和加密原语的安全边界，不依赖数据库或外部服务。
package auth

import (
	"github.com/stretchr/testify/require"
	"testing"
)

// TestHashPasswordAndVerifyPassword 验证哈希密码并Verify密码的预期行为场景。
func TestHashPasswordAndVerifyPassword(t *testing.T) {
	// 测试参数刻意降低成本，保留 PHC 格式和校验语义但避免单元测试过慢。
	params := PasswordParams{
		Memory:      32,
		Iterations:  1,
		Parallelism: 1,
		SaltLength:  8,
		KeyLength:   16,
	}

	hash, err := HashPassword("correct-password", params)
	require.NoError(t, err)
	require.True(t, VerifyPassword("correct-password", hash))
	require.False(t, VerifyPassword("wrong-password", hash))
}

// TestVerifyPasswordRejectsMalformedHash 验证Verify密码拒绝格式错误哈希的异常或拒绝路径场景。
func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	require.False(t, VerifyPassword("password", "not-a-phc-hash"))
}

// TestHashPasswordRejectsInvalidInput 验证哈希密码拒绝非法输入的异常或拒绝路径场景。
func TestHashPasswordRejectsInvalidInput(t *testing.T) {
	_, err := HashPassword("", DefaultPasswordParams)
	require.Error(t, err)
	_, err = HashPassword("password", PasswordParams{})
	require.Error(t, err)
}
