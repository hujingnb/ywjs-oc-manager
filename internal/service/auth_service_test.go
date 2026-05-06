package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

func TestAuthServiceLoginIssuesTokens(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)

	result, err := svc.Login(context.Background(), LoginInput{
		Username: "member@example.com",
		Password: "correct-password",
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if result.User.Username != "member@example.com" {
		t.Fatalf("user = %+v, want logged in user", result.User)
	}
	if result.Tokens.AccessToken == "" || result.Tokens.RefreshToken == "" {
		t.Fatal("期望登录后返回 access token 和 refresh token")
	}
	if !store.loggedIn {
		t.Fatal("期望登录成功后记录 last_login_at")
	}
	if len(store.refreshTokens) != 1 {
		t.Fatalf("refresh token count = %d, want 1", len(store.refreshTokens))
	}
}

func TestAuthServiceLoginRejectsWrongPassword(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)

	_, err := svc.Login(context.Background(), LoginInput{
		Username: "member@example.com",
		Password: "wrong-password",
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
	}
}

func TestAuthServiceLoginRejectsDisabledOrg(t *testing.T) {
	store := newAuthStoreStub(t)
	store.org.Status = domain.StatusDisabled
	svc := newTestAuthService(t, store)

	_, err := svc.Login(context.Background(), LoginInput{
		Username: "member@example.com",
		Password: "correct-password",
	})
	if !errors.Is(err, ErrOrgDisabled) {
		t.Fatalf("Login() error = %v, want ErrOrgDisabled", err)
	}
}

func TestAuthServiceRefreshRotatesRefreshToken(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)

	login, err := svc.Login(context.Background(), LoginInput{
		Username: "member@example.com",
		Password: "correct-password",
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	refreshed, err := svc.Refresh(context.Background(), login.Tokens.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if refreshed.Tokens.AccessToken == "" || refreshed.Tokens.RefreshToken == "" {
		t.Fatal("期望刷新后返回新的 token pair")
	}
	if len(store.revoked) != 1 {
		t.Fatalf("revoked count = %d, want 1", len(store.revoked))
	}
}

func TestAuthServiceLogoutIsIdempotent(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)

	if err := svc.Logout(context.Background(), "unknown-token"); err != nil {
		t.Fatalf("Logout() unknown token error = %v", err)
	}
}

// TestAuthServiceRefreshRejectsExpiredToken 校验 refresh token 在 expires_at <= now 时被拒绝；
// stub 用 SetRefreshExpiresAt 把已签发的 record.ExpiresAt 推到过去。
func TestAuthServiceRefreshRejectsExpiredToken(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)
	login, err := svc.Login(context.Background(), LoginInput{
		Username: "member@example.com",
		Password: "correct-password",
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
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
		Username: "member@example.com",
		Password: "correct-password",
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := svc.Refresh(context.Background(), login.Tokens.RefreshToken); err != nil {
		t.Fatalf("第一次 Refresh err = %v", err)
	}
	if _, err := svc.Refresh(context.Background(), login.Tokens.RefreshToken); err == nil || !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("第二次复用旧 refresh err = %v, want ErrInvalidToken", err)
	}
}

func newTestAuthService(t *testing.T, store *authStoreStub) *AuthService {
	t.Helper()
	tokens, err := auth.NewTokenManager("access-secret", "refresh-secret", time.Minute, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	svc := NewAuthService(store, tokens)
	svc.now = func() time.Time { return time.Now().UTC() }
	return svc
}

func newAuthStoreStub(t *testing.T) *authStoreStub {
	t.Helper()
	orgID := mustUUID(t, "00000000-0000-0000-0000-000000000101")
	userID := mustUUID(t, "00000000-0000-0000-0000-000000000201")
	refreshID := mustUUID(t, "00000000-0000-0000-0000-000000000301")
	hash, err := auth.HashPassword("correct-password", auth.PasswordParams{
		Memory:      32,
		Iterations:  1,
		Parallelism: 1,
		SaltLength:  8,
		KeyLength:   16,
	})
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	return &authStoreStub{
		nextRefreshID: refreshID,
		org: sqlc.Organization{
			ID:     orgID,
			Name:   "测试组织",
			Status: domain.StatusActive,
		},
		user: sqlc.User{
			ID:           userID,
			OrgID:        orgID,
			Username:     "member@example.com",
			PasswordHash: hash,
			DisplayName:  "测试成员",
			Role:         domain.UserRoleOrgMember,
			Status:       domain.StatusActive,
		},
		refreshTokens: map[string]sqlc.RefreshToken{},
	}
}

type authStoreStub struct {
	user          sqlc.User
	org           sqlc.Organization
	nextRefreshID pgtype.UUID
	idCounter     byte
	loggedIn      bool
	refreshTokens map[string]sqlc.RefreshToken
	revoked       []pgtype.UUID
}

func (s *authStoreStub) GetUserByUsername(_ context.Context, username string) (sqlc.User, error) {
	if username != s.user.Username {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return s.user, nil
}

func (s *authStoreStub) GetUser(_ context.Context, id pgtype.UUID) (sqlc.User, error) {
	if id != s.user.ID {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return s.user, nil
}

func (s *authStoreStub) MarkUserLoggedIn(_ context.Context, id pgtype.UUID) (sqlc.User, error) {
	if id != s.user.ID {
		return sqlc.User{}, pgx.ErrNoRows
	}
	s.loggedIn = true
	return s.user, nil
}

func (s *authStoreStub) GetOrganization(_ context.Context, id pgtype.UUID) (sqlc.Organization, error) {
	if id != s.org.ID {
		return sqlc.Organization{}, pgx.ErrNoRows
	}
	return s.org, nil
}

func (s *authStoreStub) CreateRefreshToken(_ context.Context, arg sqlc.CreateRefreshTokenParams) (sqlc.RefreshToken, error) {
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
	if err := id.Scan(value); err != nil {
		t.Fatalf("parse uuid %q: %v", value, err)
	}
	return id
}
