// Package auth 的测试覆盖密码、令牌和加密原语的安全边界，不依赖数据库或外部服务。
package auth

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func TestTokenManagerSignsAndVerifiesAccessToken(t *testing.T) {
	manager := newTestTokenManager(t)

	token, err := manager.SignAccessToken(Principal{UserID: "user-1", OrgID: "org-1", Role: "org_member"})
	require.NoError(t, err)

	principal, err := manager.VerifyAccessToken(token)
	require.NoError(t, err)
	if principal.UserID != "user-1" || principal.OrgID != "org-1" || principal.Role != "org_member" {
		t.Fatalf("principal = %+v, want signed values", principal)
	}
}

func TestTokenManagerRejectsTamperedToken(t *testing.T) {
	manager := newTestTokenManager(t)
	token, err := manager.SignAccessToken(Principal{UserID: "user-1", Role: "platform_admin"})
	require.NoError(t, err)

	tampered := strings.TrimSuffix(token, token[len(token)-1:]) + "x"
	_, err = manager.VerifyAccessToken(tampered)
	require.Error(t, err)
}

func TestTokenManagerRejectsExpiredToken(t *testing.T) {
	manager := newTestTokenManager(t)
	token, err := manager.SignAccessToken(Principal{UserID: "user-1", Role: "platform_admin"})
	require.NoError(t, err)

	manager.now = func() time.Time { return time.Unix(2000, 0) }
	_, err = manager.VerifyAccessToken(token)
	require.Error(t, err)
}

func TestTokenManagerRejectsWrongTokenType(t *testing.T) {
	manager := newTestTokenManager(t)
	token, err := manager.SignRefreshToken(Principal{UserID: "user-1", Role: "platform_admin"})
	require.NoError(t, err)

	_, err = manager.VerifyAccessToken(token)
	require.Error(t, err)
}

func TestNewTokenManagerValidatesConfig(t *testing.T) {
	_, err := NewTokenManager("", "refresh", time.Minute, time.Hour)
	require.Error(t, err)
	_, err = NewTokenManager("access", "refresh", 0, time.Hour)
	require.Error(t, err)
}

func newTestTokenManager(t *testing.T) *TokenManager {
	t.Helper()
	manager, err := NewTokenManager("access-secret", "refresh-secret", time.Minute, time.Hour)
	require.NoError(t, err)
	// 固定 now 让 exp/iat 和过期测试稳定，避免依赖真实时间流逝。
	manager.now = func() time.Time { return time.Unix(1000, 0) }
	return manager
}
