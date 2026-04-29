package auth

import "testing"

func TestHashPasswordAndVerifyPassword(t *testing.T) {
	params := PasswordParams{
		Memory:      32,
		Iterations:  1,
		Parallelism: 1,
		SaltLength:  8,
		KeyLength:   16,
	}

	hash, err := HashPassword("correct-password", params)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if !VerifyPassword("correct-password", hash) {
		t.Fatal("期望正确密码验证通过")
	}
	if VerifyPassword("wrong-password", hash) {
		t.Fatal("期望错误密码验证失败")
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	if VerifyPassword("password", "not-a-phc-hash") {
		t.Fatal("期望格式错误的 hash 验证失败")
	}
}

func TestHashPasswordRejectsInvalidInput(t *testing.T) {
	if _, err := HashPassword("", DefaultPasswordParams); err == nil {
		t.Fatal("期望空密码返回错误")
	}
	if _, err := HashPassword("password", PasswordParams{}); err == nil {
		t.Fatal("期望非法 Argon2id 参数返回错误")
	}
}
