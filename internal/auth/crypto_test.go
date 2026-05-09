package auth

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"github.com/stretchr/testify/require"
)

func newTestKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	return key
}

func TestNewCipher_RejectsNon32Bytes(t *testing.T) {
	for _, size := range []int{0, 1, 16, 24, 31, 33, 64} {
		_, err := NewCipher(make([]byte, size))
		require.Error(t, err)
	}
}

func TestNewCipher_AcceptsExact32Bytes(t *testing.T) {
	c, err := NewCipher(newTestKey(t))
	require.NoError(t, err)
	require.NotNil(t, c)
}

func TestCipher_RoundTrip(t *testing.T) {
	c, err := NewCipher(newTestKey(t))
	require.NoError(t, err)
	plaintexts := [][]byte{
		[]byte("hello"),
		[]byte(""),
		bytes.Repeat([]byte{0x42}, 1024),
		[]byte("中文混 ASCII test 1234"),
	}
	for _, pt := range plaintexts {
		token, err := c.Encrypt(pt)
		require.NoError(t, err)
		got, err := c.Decrypt(token)
		require.NoError(t, err)
		require.True(t, bytes.Equal(got, pt))
	}
}

func TestCipher_EncryptIsRandomized(t *testing.T) {
	c, err := NewCipher(newTestKey(t))
	require.NoError(t, err)
	a, _ := c.Encrypt([]byte("same"))
	b, _ := c.Encrypt([]byte("same"))
	require.NotEqual(t, b, a)
}

func TestCipher_DecryptRejectsTampered(t *testing.T) {
	c, err := NewCipher(newTestKey(t))
	require.NoError(t, err)
	token, _ := c.Encrypt([]byte("original"))
	raw, err := base64.StdEncoding.DecodeString(token)
	require.NoError(t, err)
	raw[len(raw)-1] ^= 0x01
	tampered := base64.StdEncoding.EncodeToString(raw)
	_, err = c.Decrypt(tampered)
	require.Error(t, err)
}

func TestCipher_DecryptRejectsBadBase64(t *testing.T) {
	c, _ := NewCipher(newTestKey(t))
	if _, err := c.Decrypt("!!!not-base64!!!"); err == nil || !strings.Contains(err.Error(), "base64") {
		t.Fatalf("Decrypt 非法 base64 err = %v, want base64 错误", err)
	}
}

func TestCipher_DecryptRejectsTooShort(t *testing.T) {
	c, _ := NewCipher(newTestKey(t))
	short := base64.StdEncoding.EncodeToString([]byte{0x00, 0x01})
	_, err := c.Decrypt(short)
	require.Error(t, err)
}

func TestCipher_DecryptRejectsCrossKey(t *testing.T) {
	keyA := newTestKey(t)
	keyB := make([]byte, 32)
	for i := range keyB {
		keyB[i] = 0xAA
	}
	cipherA, _ := NewCipher(keyA)
	cipherB, _ := NewCipher(keyB)
	token, _ := cipherA.Encrypt([]byte("for-A"))
	_, err := cipherB.Decrypt(token)
	require.Error(t, err)
}
