package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	null "github.com/guregu/null/v5"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

const (
	authTestOrgID           = "00000000-0000-0000-0000-000000000101"
	authTestPlatformAdminID = "00000000-0000-0000-0000-000000000200"
	authTestOrgAdminID      = "00000000-0000-0000-0000-000000000201"
	authTestOrgMemberID     = "00000000-0000-0000-0000-000000000202"
)

// TestAuthServiceLoginPlatformAdminWithoutOrgCode 验证认证服务登录平台管理员不使用企业标识的预期行为场景。
func TestAuthServiceLoginPlatformAdminWithoutOrgCode(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)

	result, err := svc.Login(context.Background(), LoginInput{
		Username: "admin",
		Password: "correct-password",
	})

	require.NoError(t, err)
	require.Equal(t, domain.UserRolePlatformAdmin, result.User.Role)
	require.Equal(t, "admin", result.User.Username)
	require.Empty(t, result.User.OrgID)
}

// TestAuthServiceLoginRejectsPlatformAdminWithOrgCode 验证认证服务登录拒绝平台管理员使用企业标识的异常或拒绝路径场景。
func TestAuthServiceLoginRejectsPlatformAdminWithOrgCode(t *testing.T) {
	store := newAuthStoreStub(t)
	delete(store.orgUsersByKey, orgUserKey(store.orgsByCode["test-org"].ID, "admin"))
	svc := newTestAuthService(t, store)

	_, err := svc.Login(context.Background(), LoginInput{
		OrgCode:  "test-org",
		Username: "admin",
		Password: "correct-password",
	})

	require.ErrorIs(t, err, ErrInvalidCredentials)
}

// TestAuthServiceLoginOrgUserWithOrgCode 验证认证服务登录企业用户使用企业标识的预期行为场景。
func TestAuthServiceLoginOrgUserWithOrgCode(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)

	result, err := svc.Login(context.Background(), LoginInput{
		OrgCode:  "test-org",
		Username: "admin",
		Password: "correct-password",
	})

	require.NoError(t, err)
	require.Equal(t, domain.UserRoleOrgAdmin, result.User.Role)
	require.Equal(t, "admin", result.User.Username)
	require.Equal(t, authTestOrgID, result.User.OrgID)
}

// TestAuthServiceLoginRejectsOrgUserWithoutOrgCode 验证认证服务登录拒绝企业用户不使用企业标识的异常或拒绝路径场景。
func TestAuthServiceLoginRejectsOrgUserWithoutOrgCode(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)

	_, err := svc.Login(context.Background(), LoginInput{
		Username: "member",
		Password: "correct-password",
	})

	require.ErrorIs(t, err, ErrInvalidCredentials)
}

// TestAuthServiceLoginRejectsUnknownOrgCode 验证认证服务登录拒绝未知企业标识的异常或拒绝路径场景。
func TestAuthServiceLoginRejectsUnknownOrgCode(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)

	_, err := svc.Login(context.Background(), LoginInput{
		OrgCode:  "missing-org",
		Username: "admin",
		Password: "correct-password",
	})

	require.ErrorIs(t, err, ErrInvalidCredentials)
}

// TestAuthServiceLoginIssuesTokens 验证认证服务登录IssuesTokens的预期行为场景。
func TestAuthServiceLoginIssuesTokens(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)

	result, err := svc.Login(context.Background(), LoginInput{
		OrgCode:  "test-org",
		Username: "admin",
		Password: "correct-password",
	})
	require.NoError(t, err)
	require.Equal(t, "admin", result.User.Username)
	if result.Tokens.AccessToken == "" || result.Tokens.RefreshToken == "" {
		t.Fatal("期望登录后返回 access token 和 refresh token")
	}
	require.True(t, store.loggedIn)
	require.Equal(t, 1, len(store.refreshTokens))
}

// TestAuthServiceLoginRejectsWrongPassword 验证认证服务登录拒绝错误密码的异常或拒绝路径场景。
func TestAuthServiceLoginRejectsWrongPassword(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)

	_, err := svc.Login(context.Background(), LoginInput{
		OrgCode:  "test-org",
		Username: "admin",
		Password: "wrong-password",
	})
	require.ErrorIs(t, err, ErrInvalidCredentials)
}

// TestAuthServiceLoginRejectsDisabledOrg 验证认证服务登录拒绝禁用组织的异常或拒绝路径场景。
func TestAuthServiceLoginRejectsDisabledOrg(t *testing.T) {
	store := newAuthStoreStub(t)
	org := store.orgsByCode["test-org"]
	org.Status = domain.StatusDisabled
	store.orgsByCode["test-org"] = org
	store.orgsByID[org.ID] = org
	svc := newTestAuthService(t, store)

	_, err := svc.Login(context.Background(), LoginInput{
		OrgCode:  "test-org",
		Username: "admin",
		Password: "correct-password",
	})
	require.ErrorIs(t, err, ErrOrgDisabled)
	require.ErrorContains(t, err, "企业已被禁用")
}

// TestAuthServiceRefreshRotatesRefreshToken 验证认证服务刷新Rotates刷新令牌的预期行为场景。
func TestAuthServiceRefreshRotatesRefreshToken(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)

	login, err := svc.Login(context.Background(), LoginInput{
		OrgCode:  "test-org",
		Username: "admin",
		Password: "correct-password",
	})
	require.NoError(t, err)

	refreshed, err := svc.Refresh(context.Background(), login.Tokens.RefreshToken)
	require.NoError(t, err)
	if refreshed.Tokens.AccessToken == "" || refreshed.Tokens.RefreshToken == "" {
		t.Fatal("期望刷新后返回新的 token pair")
	}
	require.Equal(t, 1, len(store.revoked))
}

// TestAuthServiceLogoutIsIdempotent 验证认证服务登出保持幂等的特殊分支或幂等场景。
func TestAuthServiceLogoutIsIdempotent(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)

	err := svc.Logout(context.Background(), "unknown-token")
	require.NoError(t, err)
}

// TestAuthServiceRefreshRejectsExpiredToken 校验 refresh token 在 expires_at <= now 时被拒绝；
// stub 用 expireAll 把已签发的 record.ExpiresAt 推到过去。
func TestAuthServiceRefreshRejectsExpiredToken(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)
	login, err := svc.Login(context.Background(), LoginInput{
		Username: "admin",
		Password: "correct-password",
	})
	require.NoError(t, err)
	store.expireAll()
	if _, err := svc.Refresh(context.Background(), login.Tokens.RefreshToken); err == nil || !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Refresh(expired) err = %v, want ErrInvalidToken", err)
	}
}

// TestAuthServiceRefreshRejectsRotatedToken 校验旧 refresh token 在被一次 Refresh 轮换后立即失效；
// 两次复用同一个 refresh 应该返回 ErrInvalidToken。
//
// 时序说明：JWT IssuedAt/ExpiresAt 精度是秒，两次签发在同一秒内会产生字节完全相同的
// refresh token → hash 撞车，stub 的 map[hash]record 会被新 record 覆盖，测试观察
// 不到"轮换后旧 refresh 失效"。两次 Refresh 之间 sleep 1.1s 跨越秒边界，让新 refresh
// 与旧 refresh 字节不同；这个延迟仅影响该单测，整体跑测仍 < 2s。
func TestAuthServiceRefreshRejectsRotatedToken(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)
	login, err := svc.Login(context.Background(), LoginInput{
		Username: "admin",
		Password: "correct-password",
	})
	require.NoError(t, err)
	time.Sleep(1100 * time.Millisecond)
	_, err = svc.Refresh(context.Background(), login.Tokens.RefreshToken)
	require.NoError(t, err)
	if _, err := svc.Refresh(context.Background(), login.Tokens.RefreshToken); err == nil || !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("第二次复用旧 refresh err = %v, want ErrInvalidToken", err)
	}
}

// TestAuthServiceChangePasswordUpdatesHash 验证登录用户输入正确旧密码后可更新自己的密码 hash。
func TestAuthServiceChangePasswordUpdatesHash(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)
	svc.hashPassword = fakeAuthHash

	err := svc.ChangePassword(context.Background(), auth.Principal{
		UserID: authTestOrgMemberID,
		OrgID:  authTestOrgID,
		Role:   domain.UserRoleOrgMember,
	}, ChangePasswordInput{
		OldPassword: "correct-password",
		NewPassword: "new-password-123",
	})

	require.NoError(t, err)
	assert.Equal(t, 1, store.updatePasswordCalls)
	assert.Equal(t, authTestOrgMemberID, store.lastPasswordUpdate.ID)
	assert.Equal(t, "hashed:new-password-123", store.lastPasswordUpdate.PasswordHash)
	assert.NotEqual(t, "new-password-123", store.usersByID[authTestOrgMemberID].PasswordHash)
	assert.Equal(t, 1, store.revokeByUserCalls)
	assert.Equal(t, []string{authTestOrgMemberID}, store.revokedByUser)
}

// TestAuthServiceChangePasswordRevokesUserRefreshTokens 验证改密成功后会撤销该用户所有未撤销的 refresh token。
func TestAuthServiceChangePasswordRevokesUserRefreshTokens(t *testing.T) {
	store := newAuthStoreStub(t)
	memberTokenHash := "member-refresh-token"
	adminTokenHash := "admin-refresh-token"
	store.refreshTokens[memberTokenHash] = sqlc.RefreshToken{
		ID:        "00000000-0000-0000-0000-000000000401",
		UserID:    authTestOrgMemberID,
		TokenHash: memberTokenHash,
		ExpiresAt: time.Now().Add(time.Hour),
	}
	store.refreshTokens[adminTokenHash] = sqlc.RefreshToken{
		ID:        "00000000-0000-0000-0000-000000000402",
		UserID:    authTestOrgAdminID,
		TokenHash: adminTokenHash,
		ExpiresAt: time.Now().Add(time.Hour),
	}
	svc := newTestAuthService(t, store)
	svc.hashPassword = fakeAuthHash

	err := svc.ChangePassword(context.Background(), auth.Principal{
		UserID: authTestOrgMemberID,
		OrgID:  authTestOrgID,
		Role:   domain.UserRoleOrgMember,
	}, ChangePasswordInput{
		OldPassword: "correct-password",
		NewPassword: "new-password-123",
	})

	require.NoError(t, err)
	assert.True(t, store.refreshTokens[memberTokenHash].RevokedAt.Valid)
	assert.False(t, store.refreshTokens[adminTokenHash].RevokedAt.Valid)
}

// TestAuthServiceChangePasswordReturnsRevokeFailure 验证密码已更新但撤销 refresh token 失败时返回明确错误。
func TestAuthServiceChangePasswordReturnsRevokeFailure(t *testing.T) {
	store := newAuthStoreStub(t)
	store.revokeByUserErr = errors.New("store unavailable")
	svc := newTestAuthService(t, store)
	svc.hashPassword = fakeAuthHash

	err := svc.ChangePassword(context.Background(), auth.Principal{
		UserID: authTestOrgMemberID,
		OrgID:  authTestOrgID,
		Role:   domain.UserRoleOrgMember,
	}, ChangePasswordInput{
		OldPassword: "correct-password",
		NewPassword: "new-password-123",
	})

	require.Error(t, err)
	require.ErrorContains(t, err, "撤销当前用户 refresh token 失败")
	assert.Equal(t, 1, store.updatePasswordCalls)
	assert.Equal(t, 1, store.revokeByUserCalls)
}

// TestAuthServiceChangePasswordRejectsWrongOldPassword 验证旧密码不匹配时拒绝修改并且不写库。
func TestAuthServiceChangePasswordRejectsWrongOldPassword(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)
	svc.hashPassword = fakeAuthHash

	err := svc.ChangePassword(context.Background(), auth.Principal{
		UserID: authTestOrgMemberID,
		OrgID:  authTestOrgID,
		Role:   domain.UserRoleOrgMember,
	}, ChangePasswordInput{
		OldPassword: "wrong-password",
		NewPassword: "new-password-123",
	})

	require.ErrorIs(t, err, ErrInvalidCredentials)
	assert.Equal(t, 0, store.updatePasswordCalls)
}

// TestAuthServiceChangePasswordRejectsInvalidNewPassword 验证新密码未通过基础规则时拒绝修改。
func TestAuthServiceChangePasswordRejectsInvalidNewPassword(t *testing.T) {
	tests := []struct {
		name        string
		oldPassword string
		newPassword string
	}{
		{name: "empty", oldPassword: "correct-password", newPassword: ""},                       // 覆盖新密码为空的输入校验。
		{name: "short", oldPassword: "correct-password", newPassword: "short"},                  // 覆盖新密码长度不足 8 位的边界。
		{name: "same_as_old", oldPassword: "correct-password", newPassword: "correct-password"}, // 覆盖新密码与当前密码相同的拒绝路径。
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			store := newAuthStoreStub(t)
			svc := newTestAuthService(t, store)
			svc.hashPassword = fakeAuthHash

			err := svc.ChangePassword(context.Background(), auth.Principal{
				UserID: authTestOrgMemberID,
				OrgID:  authTestOrgID,
				Role:   domain.UserRoleOrgMember,
			}, ChangePasswordInput{
				OldPassword: tt.oldPassword,
				NewPassword: tt.newPassword,
			})

			require.ErrorIs(t, err, ErrMemberCreateInvalid)
			assert.Equal(t, 0, store.updatePasswordCalls)
		})
	}
}

// TestAuthServiceChangePasswordRejectsDisabledUser 验证禁用用户不能自助修改密码。
func TestAuthServiceChangePasswordRejectsDisabledUser(t *testing.T) {
	store := newAuthStoreStub(t)
	user := store.usersByID[authTestOrgMemberID]
	user.Status = domain.StatusDisabled
	store.usersByID[authTestOrgMemberID] = user
	store.orgUsersByKey[orgUserKey(authTestOrgID, user.Username)] = user
	svc := newTestAuthService(t, store)
	svc.hashPassword = fakeAuthHash

	err := svc.ChangePassword(context.Background(), auth.Principal{
		UserID: authTestOrgMemberID,
		OrgID:  authTestOrgID,
		Role:   domain.UserRoleOrgMember,
	}, ChangePasswordInput{
		OldPassword: "correct-password",
		NewPassword: "new-password-123",
	})

	require.ErrorIs(t, err, ErrUserDisabled)
	assert.Equal(t, 0, store.updatePasswordCalls)
}

// TestAuthServiceChangePasswordRejectsDisabledOrg 验证所属企业禁用时组织用户不能自助修改密码。
func TestAuthServiceChangePasswordRejectsDisabledOrg(t *testing.T) {
	store := newAuthStoreStub(t)
	org := store.orgsByID[authTestOrgID]
	org.Status = domain.StatusDisabled
	store.orgsByID[authTestOrgID] = org
	store.orgsByCode[org.Code] = org
	svc := newTestAuthService(t, store)
	svc.hashPassword = fakeAuthHash

	err := svc.ChangePassword(context.Background(), auth.Principal{
		UserID: authTestOrgMemberID,
		OrgID:  authTestOrgID,
		Role:   domain.UserRoleOrgMember,
	}, ChangePasswordInput{
		OldPassword: "correct-password",
		NewPassword: "new-password-123",
	})

	require.ErrorIs(t, err, ErrOrgDisabled)
	assert.Equal(t, 0, store.updatePasswordCalls)
}

func newTestAuthService(t *testing.T, store *authStoreStub) *AuthService {
	t.Helper()
	tokens, err := auth.NewTokenManager("access-secret", "refresh-secret", time.Minute, time.Hour)
	require.NoError(t, err)
	// captcha 传 nil：既有测试默认不启用验证码校验。
	svc := NewAuthService(store, tokens, nil)
	svc.now = func() time.Time { return time.Now().UTC() }
	return svc
}

func newAuthStoreStub(t *testing.T) *authStoreStub {
	t.Helper()
	orgID := mustUUID(t, authTestOrgID)
	platformID := mustUUID(t, authTestPlatformAdminID)
	orgAdminID := mustUUID(t, authTestOrgAdminID)
	orgMemberID := mustUUID(t, authTestOrgMemberID)
	hash, err := auth.HashPassword("correct-password", auth.PasswordParams{
		Memory:      32,
		Iterations:  1,
		Parallelism: 1,
		SaltLength:  8,
		KeyLength:   16,
	})
	require.NoError(t, err)
	org := sqlc.Organization{
		ID:     orgID,
		Code:   "test-org",
		Name:   "测试组织",
		Status: domain.StatusActive,
	}
	platformAdminUser := sqlc.User{
		ID:           platformID,
		Username:     "admin",
		PasswordHash: hash,
		DisplayName:  "平台管理员",
		Role:         domain.UserRolePlatformAdmin,
		Status:       domain.StatusActive,
	}
	orgAdmin := sqlc.User{
		ID:           orgAdminID,
		OrgID:        null.StringFrom(orgID),
		Username:     "admin",
		PasswordHash: hash,
		DisplayName:  "企业管理员",
		Role:         domain.UserRoleOrgAdmin,
		Status:       domain.StatusActive,
	}
	orgMember := sqlc.User{
		ID:           orgMemberID,
		OrgID:        null.StringFrom(orgID),
		Username:     "member",
		PasswordHash: hash,
		DisplayName:  "企业成员",
		Role:         domain.UserRoleOrgMember,
		Status:       domain.StatusActive,
	}
	return &authStoreStub{
		usersByID: map[string]sqlc.User{
			platformAdminUser.ID: platformAdminUser,
			orgAdmin.ID:          orgAdmin,
			orgMember.ID:         orgMember,
		},
		platformByName: map[string]sqlc.User{
			platformAdminUser.Username: platformAdminUser,
		},
		orgUsersByKey: map[string]sqlc.User{
			orgUserKey(org.ID, orgAdmin.Username):  orgAdmin,
			orgUserKey(org.ID, orgMember.Username): orgMember,
		},
		orgsByID: map[string]sqlc.Organization{
			org.ID: org,
		},
		orgsByCode: map[string]sqlc.Organization{
			org.Code: org,
		},
		// 每次创建 refresh token 用递增计数器派生唯一 ID。
		nextRefreshCounter: 0,
		refreshTokens:      map[string]sqlc.RefreshToken{},
	}
}

type authStoreStub struct {
	usersByID      map[string]sqlc.User
	platformByName map[string]sqlc.User
	orgUsersByKey  map[string]sqlc.User
	orgsByID       map[string]sqlc.Organization
	orgsByCode     map[string]sqlc.Organization
	// nextRefreshCounter 用于生成唯一 refresh token ID 的自增计数。
	nextRefreshCounter int
	loggedIn           bool
	lastIssuedRole     string
	refreshTokens      map[string]sqlc.RefreshToken
	revoked            []string
	// revokedByUser 记录按用户撤销 refresh token 的入参，覆盖改密后的会话失效场景。
	revokedByUser     []string
	revokeByUserCalls int
	revokeByUserErr   error
	// lastPasswordUpdate 和 updatePasswordCalls 记录改密写库入参与调用次数，便于断言失败路径不落库。
	lastPasswordUpdate  sqlc.UpdateUserPasswordParams
	updatePasswordCalls int
}

// orgUserKey 拼接组织 ID（string）和用户名作为 stub map key。
func orgUserKey(orgID string, username string) string {
	return orgID + "/" + username
}

func (s *authStoreStub) GetUserByUsername(_ context.Context, username string) (sqlc.User, error) {
	user, ok := s.platformByName[username]
	if !ok {
		return sqlc.User{}, sql.ErrNoRows
	}
	return user, nil
}

func (s *authStoreStub) GetUserByOrgAndUsername(_ context.Context, arg sqlc.GetUserByOrgAndUsernameParams) (sqlc.User, error) {
	user, ok := s.orgUsersByKey[orgUserKey(arg.OrgID.String, arg.Username)]
	if !ok {
		return sqlc.User{}, sql.ErrNoRows
	}
	return user, nil
}

func (s *authStoreStub) GetUser(_ context.Context, id string) (sqlc.User, error) {
	user, ok := s.usersByID[id]
	if !ok {
		return sqlc.User{}, sql.ErrNoRows
	}
	return user, nil
}

func (s *authStoreStub) MarkUserLoggedIn(_ context.Context, id string) error {
	_, ok := s.usersByID[id]
	if !ok {
		return sql.ErrNoRows
	}
	s.loggedIn = true
	return nil
}

// UpdateUserPassword 模拟 users.password_hash 写入，并同步维护按用户名查询的索引。
func (s *authStoreStub) UpdateUserPassword(_ context.Context, arg sqlc.UpdateUserPasswordParams) error {
	s.lastPasswordUpdate = arg
	user, ok := s.usersByID[arg.ID]
	if !ok {
		return sql.ErrNoRows
	}
	s.updatePasswordCalls++
	user.PasswordHash = arg.PasswordHash
	s.usersByID[arg.ID] = user
	if user.OrgID.Valid {
		s.orgUsersByKey[orgUserKey(user.OrgID.String, user.Username)] = user
	} else {
		s.platformByName[user.Username] = user
	}
	return nil
}

func (s *authStoreStub) GetOrganization(_ context.Context, id string) (sqlc.Organization, error) {
	org, ok := s.orgsByID[id]
	if !ok {
		return sqlc.Organization{}, sql.ErrNoRows
	}
	return org, nil
}

func (s *authStoreStub) GetOrganizationByCode(_ context.Context, code string) (sqlc.Organization, error) {
	org, ok := s.orgsByCode[code]
	if !ok {
		return sqlc.Organization{}, sql.ErrNoRows
	}
	return org, nil
}

func (s *authStoreStub) CreateRefreshToken(_ context.Context, arg sqlc.CreateRefreshTokenParams) error {
	if _, exists := s.refreshTokens[arg.TokenHash]; exists {
		return errors.New("refresh token hash 重复")
	}
	// 每次创建生成不同 ID，避免 RevokeRefreshToken 用 ID 反查时命中历史记录。
	s.nextRefreshCounter++
	id := "00000000-0000-0000-0000-0000000003" + string(rune('0'+s.nextRefreshCounter%10))
	record := sqlc.RefreshToken{
		ID:        id,
		UserID:    arg.UserID,
		TokenHash: arg.TokenHash,
		ExpiresAt: arg.ExpiresAt,
	}
	s.refreshTokens[arg.TokenHash] = record
	return nil
}

func (s *authStoreStub) GetRefreshTokenByHash(_ context.Context, tokenHash string) (sqlc.RefreshToken, error) {
	record, ok := s.refreshTokens[tokenHash]
	if !ok {
		return sqlc.RefreshToken{}, sql.ErrNoRows
	}
	return record, nil
}

// expireAll 把 stub 中所有 refresh token 的 expires_at 推到过去，模拟过期场景。
func (s *authStoreStub) expireAll() {
	for hash, record := range s.refreshTokens {
		record.ExpiresAt = time.Now().Add(-time.Hour)
		s.refreshTokens[hash] = record
	}
}

func (s *authStoreStub) RevokeRefreshToken(_ context.Context, id string) error {
	for hash, record := range s.refreshTokens {
		if record.ID == id {
			record.RevokedAt = null.TimeFrom(time.Now())
			s.refreshTokens[hash] = record
			s.revoked = append(s.revoked, id)
			return nil
		}
	}
	return sql.ErrNoRows
}

// RevokeRefreshTokensByUser 模拟按用户撤销所有仍有效 refresh token 的写库行为。
func (s *authStoreStub) RevokeRefreshTokensByUser(_ context.Context, userID string) error {
	s.revokeByUserCalls++
	s.revokedByUser = append(s.revokedByUser, userID)
	if s.revokeByUserErr != nil {
		return s.revokeByUserErr
	}
	for hash, record := range s.refreshTokens {
		if record.UserID == userID && !record.RevokedAt.Valid {
			record.RevokedAt = null.TimeFrom(time.Now())
			s.refreshTokens[hash] = record
		}
	}
	return nil
}

// UpdateUserLocale 是 AuthStore 接口测试桩实现；既有测试不调用此方法，故为空操作。
func (s *authStoreStub) UpdateUserLocale(_ context.Context, _ sqlc.UpdateUserLocaleParams) error {
	return nil
}

// GetActiveAppByOwner 是 AuthStore 接口测试桩实现；既有测试不调用此方法，返回 sql.ErrNoRows。
func (s *authStoreStub) GetActiveAppByOwner(_ context.Context, _ string) (sqlc.App, error) {
	return sqlc.App{}, sql.ErrNoRows
}

// UpdateAppLocale 是 AuthStore 接口测试桩实现；既有测试不调用此方法，故为空操作。
func (s *authStoreStub) UpdateAppLocale(_ context.Context, _ sqlc.UpdateAppLocaleParams) error {
	return nil
}

// loginFakeCaptcha 是 CaptchaVerifier 的测试桩，按预置 err 返回。
type loginFakeCaptcha struct{ err error }

func (f loginFakeCaptcha) Verify(_ context.Context, _ string) error { return f.err }

// 验证码校验失败时，Login 直接返回该错误且不进入密码校验。
func TestAuthServiceLoginRejectsBadCaptcha(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)
	svc.captcha = loginFakeCaptcha{err: ErrCaptchaInvalid} // 注入失败桩

	_, err := svc.Login(context.Background(), LoginInput{
		OrgCode:  "test-org",
		Username: "admin",
		Password: "correct-password",
		Captcha:  "whatever",
	})
	require.ErrorIs(t, err, ErrCaptchaInvalid)
	require.False(t, store.loggedIn) // 未走到 MarkUserLoggedIn，证明前置拦截
}

// 验证码校验通过时，Login 正常签发 token。
func TestAuthServiceLoginPassesWithGoodCaptcha(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)
	svc.captcha = loginFakeCaptcha{err: nil} // 注入通过桩

	result, err := svc.Login(context.Background(), LoginInput{
		OrgCode:  "test-org",
		Username: "admin",
		Password: "correct-password",
		Captcha:  "valid",
	})
	require.NoError(t, err)
	require.Equal(t, "admin", result.User.Username)
}

// fakeLocaleStore 实现 UpdateLocale 及实例语言传播测试所需的存储方法。
// 嵌入 AuthStore 接口（nil 实现），以满足 AuthStore 的完整方法集；
// 未覆盖的方法若被调用会 panic，测试中不会触发。
type fakeLocaleStore struct {
	AuthStore
	// UpdateUserLocale 记录字段
	gotID, gotLocale string
	userLocaleErr    error
	// GetActiveAppByOwner 预置返回值
	activeApp    sqlc.App
	activeAppErr error
	// UpdateAppLocale 捕获入参与调用次数
	appLocaleParams     sqlc.UpdateAppLocaleParams
	updateAppLocaleCalls int
	updateAppLocaleErr  error
}

func (f *fakeLocaleStore) UpdateUserLocale(_ context.Context, arg sqlc.UpdateUserLocaleParams) error {
	f.gotID, f.gotLocale = arg.ID, arg.Locale.String
	return f.userLocaleErr
}

// GetActiveAppByOwner 返回预置的活跃实例或错误（如 sql.ErrNoRows）。
func (f *fakeLocaleStore) GetActiveAppByOwner(_ context.Context, _ string) (sqlc.App, error) {
	return f.activeApp, f.activeAppErr
}

// UpdateAppLocale 捕获入参并累计调用次数，用于断言传播行为。
func (f *fakeLocaleStore) UpdateAppLocale(_ context.Context, arg sqlc.UpdateAppLocaleParams) error {
	f.updateAppLocaleCalls++
	f.appLocaleParams = arg
	return f.updateAppLocaleErr
}

// TestAuthService_UpdateLocale 覆盖语言持久化：合法 locale 写库、非法 locale 被拒。
func TestAuthService_UpdateLocale(t *testing.T) {
	// 合法 zh：应写入对应用户行（传播到活跃实例）
	store := &fakeLocaleStore{activeApp: sqlc.App{ID: "app1"}}
	svc := &AuthService{store: store}
	require.NoError(t, svc.UpdateLocale(context.Background(), "u1", "zh"))
	assert.Equal(t, "u1", store.gotID)
	assert.Equal(t, "zh", store.gotLocale)

	// 非法 fr：应返回 ErrInvalidLocale，不写库
	store2 := &fakeLocaleStore{}
	svc2 := &AuthService{store: store2}
	require.ErrorIs(t, svc2.UpdateLocale(context.Background(), "u1", "fr"), ErrInvalidLocale)
	assert.Equal(t, "", store2.gotID)
}

// TestUpdateLocalePropagatesToOwnerApp 验证改界面语言后 owner 活跃实例 apps.locale 同步更新，且不触发重启。
func TestUpdateLocalePropagatesToOwnerApp(t *testing.T) {
	// fake store：UpdateUserLocale 记录入参；GetActiveAppByOwner 返回 app1；UpdateAppLocale 捕获入参
	store := &fakeLocaleStore{
		activeApp: sqlc.App{ID: "app1"},
	}
	svc := &AuthService{store: store}

	// 调用 UpdateLocale，语言 "en"
	err := svc.UpdateLocale(context.Background(), "user1", "en")
	require.NoError(t, err)

	// UpdateAppLocale 应被调用一次，入参 ID=app1、Locale=en
	assert.Equal(t, 1, store.updateAppLocaleCalls, "UpdateAppLocale 应被精确调用一次")
	assert.Equal(t, "app1", store.appLocaleParams.ID)
	assert.Equal(t, null.StringFrom("en"), store.appLocaleParams.Locale)
}

// TestUpdateLocaleNoAppIsOK 验证用户无活跃实例时 UpdateLocale 不报错、仅更新 users.locale。
func TestUpdateLocaleNoAppIsOK(t *testing.T) {
	// GetActiveAppByOwner 返回 sql.ErrNoRows，模拟用户尚无实例
	store := &fakeLocaleStore{
		activeAppErr: sql.ErrNoRows,
	}
	svc := &AuthService{store: store}

	// 应 NoError，不调 UpdateAppLocale
	err := svc.UpdateLocale(context.Background(), "user1", "zh")
	require.NoError(t, err)
	assert.Equal(t, 0, store.updateAppLocaleCalls, "无实例时不应调用 UpdateAppLocale")
}

// TestUpdateLocaleRejectsInvalidStillWorks 回归验证非法 locale 仍按原样被拒，不触发任何 store 写入。
func TestUpdateLocaleRejectsInvalidStillWorks(t *testing.T) {
	// "fr" 不在 SupportedLocales，应立即返回 ErrInvalidLocale，不调任何 store 方法
	store := &fakeLocaleStore{}
	svc := &AuthService{store: store}
	err := svc.UpdateLocale(context.Background(), "user1", "fr")
	require.ErrorIs(t, err, ErrInvalidLocale)
	assert.Equal(t, "", store.gotID, "非法 locale 不应调用 UpdateUserLocale")
	assert.Equal(t, 0, store.updateAppLocaleCalls, "非法 locale 不应调用 UpdateAppLocale")
}

// mustUUID 返回字符串 UUID（MySQL 侧 CHAR(36)，无需解析）。
func mustUUID(t *testing.T, value string) string {
	t.Helper()
	return value
}

// uuidToString 在 MySQL 侧 ID 已经是 string 后，作为向前兼容的 identity 函数保留。
func uuidToString(id string) string { return id }

// fakeAuthHash 为修改密码测试提供确定性的 hash 结果，避免引入 Argon2 成本。
func fakeAuthHash(password string) (string, error) {
	return "hashed:" + password, nil
}

// TestToAuthUser_LocaleMapping 覆盖 toAuthUser 把 users.locale(null.String) 正确映射到 AuthUser.Locale：
// 已选语言透传，NULL（未选择）映射为空字符串，交由前端回退平台默认。
func TestToAuthUser_LocaleMapping(t *testing.T) {
	// 已显式选择 zh：应原样透传
	got := toAuthUser(sqlc.User{ID: "u1", Username: "a", DisplayName: "A", Role: "org_member", Status: "active", Locale: null.StringFrom("zh")})
	assert.Equal(t, "zh", got.Locale)

	// locale 为 NULL（未选择）：应映射为空字符串
	got = toAuthUser(sqlc.User{ID: "u2", Username: "b", DisplayName: "B", Role: "org_member", Status: "active"})
	assert.Equal(t, "", got.Locale)
}
