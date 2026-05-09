package auth

import (
	"testing"
	"github.com/stretchr/testify/require"
)

func TestHashPasswordAndVerifyPassword(t *testing.T) {
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
