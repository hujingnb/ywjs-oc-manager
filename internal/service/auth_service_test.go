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
	record := sqlc.RefreshToken{
		ID:        s.nextRefreshID,
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
