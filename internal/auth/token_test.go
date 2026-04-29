package auth

import (
	"strings"
	"testing"
	"time"
)

func TestTokenManagerSignsAndVerifiesAccessToken(t *testing.T) {
	manager := newTestTokenManager(t)

	token, err := manager.SignAccessToken(Principal{UserID: "user-1", OrgID: "org-1", Role: "org_member"})
	if err != nil {
		t.Fatalf("SignAccessToken() error = %v", err)
	}

	principal, err := manager.VerifyAccessToken(token)
	if err != nil {
		t.Fatalf("VerifyAccessToken() error = %v", err)
	}
	if principal.UserID != "user-1" || principal.OrgID != "org-1" || principal.Role != "org_member" {
		t.Fatalf("principal = %+v, want signed values", principal)
	}
}

func TestTokenManagerRejectsTamperedToken(t *testing.T) {
	manager := newTestTokenManager(t)
	token, err := manager.SignAccessToken(Principal{UserID: "user-1", Role: "platform_admin"})
	if err != nil {
		t.Fatalf("SignAccessToken() error = %v", err)
	}

	tampered := strings.TrimSuffix(token, token[len(token)-1:]) + "x"
	if _, err := manager.VerifyAccessToken(tampered); err == nil {
		t.Fatal("期望篡改签名的 token 被拒绝")
	}
}

func TestTokenManagerRejectsExpiredToken(t *testing.T) {
	manager := newTestTokenManager(t)
	token, err := manager.SignAccessToken(Principal{UserID: "user-1", Role: "platform_admin"})
	if err != nil {
		t.Fatalf("SignAccessToken() error = %v", err)
	}

	manager.now = func() time.Time { return time.Unix(2000, 0) }
	if _, err := manager.VerifyAccessToken(token); err == nil {
		t.Fatal("期望过期 token 被拒绝")
	}
}

func TestTokenManagerRejectsWrongTokenType(t *testing.T) {
	manager := newTestTokenManager(t)
	token, err := manager.SignRefreshToken(Principal{UserID: "user-1", Role: "platform_admin"})
	if err != nil {
		t.Fatalf("SignRefreshToken() error = %v", err)
	}

	if _, err := manager.VerifyAccessToken(token); err == nil {
		t.Fatal("期望 refresh token 不能作为 access token 使用")
	}
}

func TestNewTokenManagerValidatesConfig(t *testing.T) {
	if _, err := NewTokenManager("", "refresh", time.Minute, time.Hour); err == nil {
		t.Fatal("期望空 access secret 返回错误")
	}
	if _, err := NewTokenManager("access", "refresh", 0, time.Hour); err == nil {
		t.Fatal("期望非法 TTL 返回错误")
	}
}

func newTestTokenManager(t *testing.T) *TokenManager {
	t.Helper()
	manager, err := NewTokenManager("access-secret", "refresh-secret", time.Minute, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	manager.now = func() time.Time { return time.Unix(1000, 0) }
	return manager
}
