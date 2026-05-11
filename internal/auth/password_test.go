// Package auth 的测试覆盖密码、令牌和加密原语的安全边界，不依赖数据库或外部服务。
package auth

import (
	"github.com/stretchr/testify/require"
	"testing"
)

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

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	require.False(t, VerifyPassword("password", "not-a-phc-hash"))
}

func TestHashPasswordRejectsInvalidInput(t *testing.T) {
	_, err := HashPassword("", DefaultPasswordParams)
	require.Error(t, err)
	_, err = HashPassword("password", PasswordParams{})
	require.Error(t, err)
}
