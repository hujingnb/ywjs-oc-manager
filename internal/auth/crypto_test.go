package auth

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
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
		if err == nil {
			t.Fatalf("NewCipher(%d 字节) err = nil, want 错误", size)
		}
	}
}

func TestNewCipher_AcceptsExact32Bytes(t *testing.T) {
	c, err := NewCipher(newTestKey(t))
	if err != nil {
		t.Fatalf("NewCipher err = %v, want nil", err)
	}
	if c == nil {
		t.Fatal("Cipher 为 nil")
	}
}

func TestCipher_RoundTrip(t *testing.T) {
	c, err := NewCipher(newTestKey(t))
	if err != nil {
		t.Fatalf("NewCipher err = %v", err)
	}
	plaintexts := [][]byte{
		[]byte("hello"),
		[]byte(""),
		bytes.Repeat([]byte{0x42}, 1024),
		[]byte("中文混 ASCII test 1234"),
	}
	for _, pt := range plaintexts {
		token, err := c.Encrypt(pt)
		if err != nil {
			t.Fatalf("Encrypt(%q) err = %v", pt, err)
		}
		got, err := c.Decrypt(token)
		if err != nil {
			t.Fatalf("Decrypt err = %v", err)
		}
		if !bytes.Equal(got, pt) {
			t.Fatalf("Decrypt = %q, want %q", got, pt)
		}
	}
}

func TestCipher_EncryptIsRandomized(t *testing.T) {
	c, err := NewCipher(newTestKey(t))
	if err != nil {
		t.Fatalf("NewCipher err = %v", err)
	}
	a, _ := c.Encrypt([]byte("same"))
	b, _ := c.Encrypt([]byte("same"))
	if a == b {
		t.Fatalf("两次加密结果一致，说明 nonce 未随机化: %q", a)
	}
}

func TestCipher_DecryptRejectsTampered(t *testing.T) {
	c, err := NewCipher(newTestKey(t))
	if err != nil {
		t.Fatalf("NewCipher err = %v", err)
	}
	token, _ := c.Encrypt([]byte("original"))
	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("解码 token: %v", err)
	}
	raw[len(raw)-1] ^= 0x01
	tampered := base64.StdEncoding.EncodeToString(raw)
	if _, err := c.Decrypt(tampered); err == nil {
		t.Fatal("Decrypt 篡改密文应失败但成功")
	}
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
	if _, err := c.Decrypt(short); err == nil {
		t.Fatal("Decrypt 过短密文应失败")
	}
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
	if _, err := cipherB.Decrypt(token); err == nil {
		t.Fatal("用其他 key 解密应失败")
	}
}
