package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

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

// TestAuthServiceLoginPlatformAdminWithoutOrgCode 验证认证服务登录平台管理员不使用组织标识的预期行为场景。
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

// TestAuthServiceLoginRejectsPlatformAdminWithOrgCode 验证认证服务登录拒绝平台管理员使用组织标识的异常或拒绝路径场景。
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

// TestAuthServiceLoginOrgUserWithOrgCode 验证认证服务登录组织用户使用组织标识的预期行为场景。
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

// TestAuthServiceLoginRejectsOrgUserWithoutOrgCode 验证认证服务登录拒绝组织用户不使用组织标识的异常或拒绝路径场景。
func TestAuthServiceLoginRejectsOrgUserWithoutOrgCode(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)

	_, err := svc.Login(context.Background(), LoginInput{
		Username: "member",
		Password: "correct-password",
	})

	require.ErrorIs(t, err, ErrInvalidCredentials)
}

// TestAuthServiceLoginRejectsUnknownOrgCode 验证认证服务登录拒绝未知组织标识的异常或拒绝路径场景。
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
	store.orgsByID[uuidToString(org.ID)] = org
	svc := newTestAuthService(t, store)

	_, err := svc.Login(context.Background(), LoginInput{
		OrgCode:  "test-org",
		Username: "admin",
		Password: "correct-password",
	})
	require.ErrorIs(t, err, ErrOrgDisabled)
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
// stub 用 SetRefreshExpiresAt 把已签发的 record.ExpiresAt 推到过去。
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

func newTestAuthService(t *testing.T, store *authStoreStub) *AuthService {
	t.Helper()
	tokens, err := auth.NewTokenManager("access-secret", "refresh-secret", time.Minute, time.Hour)
	require.NoError(t, err)
	svc := NewAuthService(store, tokens)
	svc.now = func() time.Time { return time.Now().UTC() }
	return svc
}

func newAuthStoreStub(t *testing.T) *authStoreStub {
	t.Helper()
	orgID := mustUUID(t, authTestOrgID)
	platformID := mustUUID(t, authTestPlatformAdminID)
	orgAdminID := mustUUID(t, authTestOrgAdminID)
	orgMemberID := mustUUID(t, authTestOrgMemberID)
	refreshID := mustUUID(t, "00000000-0000-0000-0000-000000000301")
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
	platformAdmin := sqlc.User{
		ID:           platformID,
		Username:     "admin",
		PasswordHash: hash,
		DisplayName:  "平台管理员",
		Role:         domain.UserRolePlatformAdmin,
		Status:       domain.StatusActive,
	}
	orgAdmin := sqlc.User{
		ID:           orgAdminID,
		OrgID:        orgID,
		Username:     "admin",
		PasswordHash: hash,
		DisplayName:  "组织管理员",
		Role:         domain.UserRoleOrgAdmin,
		Status:       domain.StatusActive,
	}
	orgMember := sqlc.User{
		ID:           orgMemberID,
		OrgID:        orgID,
		Username:     "member",
		PasswordHash: hash,
		DisplayName:  "组织成员",
		Role:         domain.UserRoleOrgMember,
		Status:       domain.StatusActive,
	}
	return &authStoreStub{
		usersByID: map[string]sqlc.User{
			uuidToString(platformAdmin.ID): platformAdmin,
			uuidToString(orgAdmin.ID):      orgAdmin,
			uuidToString(orgMember.ID):     orgMember,
		},
		platformByName: map[string]sqlc.User{
			platformAdmin.Username: platformAdmin,
		},
		orgUsersByKey: map[string]sqlc.User{
			orgUserKey(org.ID, orgAdmin.Username):  orgAdmin,
			orgUserKey(org.ID, orgMember.Username): orgMember,
		},
		orgsByID: map[string]sqlc.Organization{
			uuidToString(org.ID): org,
		},
		orgsByCode: map[string]sqlc.Organization{
			org.Code: org,
		},
		nextRefreshID: refreshID,
		refreshTokens: map[string]sqlc.RefreshToken{},
	}
}

type authStoreStub struct {
	usersByID      map[string]sqlc.User
	platformByName map[string]sqlc.User
	orgUsersByKey  map[string]sqlc.User
	orgsByID       map[string]sqlc.Organization
	orgsByCode     map[string]sqlc.Organization
	nextRefreshID  pgtype.UUID
	idCounter      byte
	loggedIn       bool
	lastIssuedRole string
	refreshTokens  map[string]sqlc.RefreshToken
	revoked        []pgtype.UUID
}

func orgUserKey(orgID pgtype.UUID, username string) string {
	return uuidToString(orgID) + "/" + username
}

func (s *authStoreStub) GetUserByUsername(_ context.Context, username string) (sqlc.User, error) {
	user, ok := s.platformByName[username]
	if !ok {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return user, nil
}

func (s *authStoreStub) GetUserByOrgAndUsername(_ context.Context, arg sqlc.GetUserByOrgAndUsernameParams) (sqlc.User, error) {
	user, ok := s.orgUsersByKey[orgUserKey(arg.OrgID, arg.Username)]
	if !ok {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return user, nil
}

func (s *authStoreStub) GetUser(_ context.Context, id pgtype.UUID) (sqlc.User, error) {
	user, ok := s.usersByID[uuidToString(id)]
	if !ok {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return user, nil
}

func (s *authStoreStub) MarkUserLoggedIn(_ context.Context, id pgtype.UUID) (sqlc.User, error) {
	user, ok := s.usersByID[uuidToString(id)]
	if !ok {
		return sqlc.User{}, pgx.ErrNoRows
	}
	s.loggedIn = true
	s.lastIssuedRole = user.Role
	return user, nil
}

func (s *authStoreStub) GetOrganization(_ context.Context, id pgtype.UUID) (sqlc.Organization, error) {
	org, ok := s.orgsByID[uuidToString(id)]
	if !ok {
		return sqlc.Organization{}, pgx.ErrNoRows
	}
	return org, nil
}

func (s *authStoreStub) GetOrganizationByCode(_ context.Context, code string) (sqlc.Organization, error) {
	org, ok := s.orgsByCode[code]
	if !ok {
		return sqlc.Organization{}, pgx.ErrNoRows
	}
	return org, nil
}

func (s *authStoreStub) CreateRefreshToken(_ context.Context, arg sqlc.CreateRefreshTokenParams) (sqlc.RefreshToken, error) {
	if _, exists := s.refreshTokens[arg.TokenHash]; exists {
		return sqlc.RefreshToken{}, errors.New("refresh token hash 重复")
	}
	// 每次创建生成不同 UUID，否则 RevokeRefreshToken 用 ID 反查时会随机命中
	// 历史 record，让"轮换后旧 token 失效"的测试断言不稳定。
	id := s.nextRefreshID
	id.Bytes[15] += s.idCounter
	s.idCounter++
	record := sqlc.RefreshToken{
		ID:        id,
		UserID:    arg.UserID,
		TokenHash: arg.TokenHash,
		ExpiresAt: arg.ExpiresAt,
	}
	s.refreshTokens[arg.TokenHash] = record
	return record, nil
}

func (s *authStoreStub) GetRefreshTokenByHash(_ context.Context, tokenHash string) (sqlc.RefreshToken, error) {
	record, ok := s.refreshTokens[tokenHash]
	if !ok {
		return sqlc.RefreshToken{}, pgx.ErrNoRows
	}
	return record, nil
}

// expireAll 把 stub 中所有 refresh token 的 expires_at 推到过去，模拟过期场景。
func (s *authStoreStub) expireAll() {
	for hash, record := range s.refreshTokens {
		record.ExpiresAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}
		s.refreshTokens[hash] = record
	}
}

func (s *authStoreStub) RevokeRefreshToken(_ context.Context, id pgtype.UUID) (sqlc.RefreshToken, error) {
	for hash, record := range s.refreshTokens {
		if record.ID == id {
			record.RevokedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			s.refreshTokens[hash] = record
			s.revoked = append(s.revoked, id)
			return record, nil
		}
	}
	return sqlc.RefreshToken{}, pgx.ErrNoRows
}

func mustUUID(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	err := id.Scan(value)
	require.NoError(t, err)
	return id
}
