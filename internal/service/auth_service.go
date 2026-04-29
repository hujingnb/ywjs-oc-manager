package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

var (
	// ErrInvalidCredentials 对外统一表示登录失败，避免泄露用户名是否存在或密码是否错误。
	ErrInvalidCredentials = errors.New("用户名或密码错误")
	ErrUserDisabled       = errors.New("用户已被禁用")
	ErrOrgDisabled        = errors.New("组织已被禁用")
	ErrInvalidToken       = errors.New("登录凭证无效")
)

// AuthStore 抽象认证流程所需的数据访问能力，便于 service 单元测试使用内存桩。
type AuthStore interface {
	GetUserByUsername(ctx context.Context, username string) (sqlc.User, error)
	GetUser(ctx context.Context, id pgtype.UUID) (sqlc.User, error)
	MarkUserLoggedIn(ctx context.Context, id pgtype.UUID) (sqlc.User, error)
	GetOrganization(ctx context.Context, id pgtype.UUID) (sqlc.Organization, error)
	CreateRefreshToken(ctx context.Context, arg sqlc.CreateRefreshTokenParams) (sqlc.RefreshToken, error)
	GetRefreshTokenByHash(ctx context.Context, tokenHash string) (sqlc.RefreshToken, error)
	RevokeRefreshToken(ctx context.Context, id pgtype.UUID) (sqlc.RefreshToken, error)
}

// AuthService 处理登录、刷新和注销等认证业务。
type AuthService struct {
	store        AuthStore
	tokens       *auth.TokenManager
	passwordHash func(string, string) bool
	now          func() time.Time
}

// NewAuthService 创建认证服务。
func NewAuthService(store AuthStore, tokens *auth.TokenManager) *AuthService {
	return &AuthService{
		store:        store,
		tokens:       tokens,
		passwordHash: auth.VerifyPassword,
		now:          time.Now,
	}
}

type LoginInput struct {
	Username string
	Password string
}

type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type AuthUser struct {
	ID          string `json:"id"`
	OrgID       string `json:"org_id,omitempty"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Status      string `json:"status"`
}

type LoginResult struct {
	User   AuthUser  `json:"user"`
	Tokens TokenPair `json:"tokens"`
}

// Login 校验用户名密码，签发 access/refresh token，并持久化 refresh token hash。
func (s *AuthService) Login(ctx context.Context, input LoginInput) (LoginResult, error) {
	user, err := s.store.GetUserByUsername(ctx, input.Username)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return LoginResult{}, ErrInvalidCredentials
		}
		return LoginResult{}, fmt.Errorf("查询用户失败: %w", err)
	}
	if !s.passwordHash(input.Password, user.PasswordHash) {
		return LoginResult{}, ErrInvalidCredentials
	}
	if err := s.ensureUserEnabled(ctx, user); err != nil {
		return LoginResult{}, err
	}

	if _, err := s.store.MarkUserLoggedIn(ctx, user.ID); err != nil {
		return LoginResult{}, fmt.Errorf("更新登录时间失败: %w", err)
	}
	return s.issueTokenPair(ctx, user)
}

// Me 根据 access token 中的主体加载最新用户状态。
func (s *AuthService) Me(ctx context.Context, principal auth.Principal) (AuthUser, error) {
	userID, err := parseUUID(principal.UserID)
	if err != nil {
		return AuthUser{}, ErrInvalidToken
	}
	user, err := s.store.GetUser(ctx, userID)
	if err != nil {
		return AuthUser{}, fmt.Errorf("查询当前用户失败: %w", err)
	}
	if err := s.ensureUserEnabled(ctx, user); err != nil {
		return AuthUser{}, err
	}
	return toAuthUser(user), nil
}

// Refresh 校验 refresh token，撤销旧 token，再签发新的 token pair。
func (s *AuthService) Refresh(ctx context.Context, refreshToken string) (LoginResult, error) {
	principal, err := s.tokens.VerifyRefreshToken(refreshToken)
	if err != nil {
		return LoginResult{}, ErrInvalidToken
	}

	record, err := s.store.GetRefreshTokenByHash(ctx, auth.HashOpaqueToken(refreshToken))
	if err != nil {
		return LoginResult{}, ErrInvalidToken
	}
	if record.RevokedAt.Valid || !record.ExpiresAt.Valid || !record.ExpiresAt.Time.After(s.now()) {
		return LoginResult{}, ErrInvalidToken
	}

	userID, err := parseUUID(principal.UserID)
	if err != nil {
		return LoginResult{}, ErrInvalidToken
	}
	user, err := s.store.GetUser(ctx, userID)
	if err != nil {
		return LoginResult{}, fmt.Errorf("查询刷新用户失败: %w", err)
	}
	if err := s.ensureUserEnabled(ctx, user); err != nil {
		return LoginResult{}, err
	}
	if _, err := s.store.RevokeRefreshToken(ctx, record.ID); err != nil {
		return LoginResult{}, fmt.Errorf("撤销旧 refresh token 失败: %w", err)
	}
	return s.issueTokenPair(ctx, user)
}

// Logout 撤销 refresh token；重复注销或 token 不存在按成功处理，保证接口幂等。
func (s *AuthService) Logout(ctx context.Context, refreshToken string) error {
	record, err := s.store.GetRefreshTokenByHash(ctx, auth.HashOpaqueToken(refreshToken))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("查询 refresh token 失败: %w", err)
	}
	if record.RevokedAt.Valid {
		return nil
	}
	if _, err := s.store.RevokeRefreshToken(ctx, record.ID); err != nil {
		return fmt.Errorf("撤销 refresh token 失败: %w", err)
	}
	return nil
}

func (s *AuthService) issueTokenPair(ctx context.Context, user sqlc.User) (LoginResult, error) {
	principal := auth.Principal{
		UserID: uuidToString(user.ID),
		OrgID:  uuidToOptionalString(user.OrgID),
		Role:   user.Role,
	}
	accessToken, err := s.tokens.SignAccessToken(principal)
	if err != nil {
		return LoginResult{}, fmt.Errorf("签发 access token 失败: %w", err)
	}
	refreshToken, err := s.tokens.SignRefreshToken(principal)
	if err != nil {
		return LoginResult{}, fmt.Errorf("签发 refresh token 失败: %w", err)
	}
	if _, err := s.store.CreateRefreshToken(ctx, sqlc.CreateRefreshTokenParams{
		UserID:    user.ID,
		TokenHash: auth.HashOpaqueToken(refreshToken),
		ExpiresAt: pgtype.Timestamptz{
			Time:  s.now().Add(s.tokens.RefreshTTL()),
			Valid: true,
		},
	}); err != nil {
		return LoginResult{}, fmt.Errorf("保存 refresh token 失败: %w", err)
	}
	return LoginResult{
		User: toAuthUser(user),
		Tokens: TokenPair{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
		},
	}, nil
}

func (s *AuthService) ensureUserEnabled(ctx context.Context, user sqlc.User) error {
	if user.Status != domain.StatusActive {
		return ErrUserDisabled
	}
	if user.OrgID.Valid {
		org, err := s.store.GetOrganization(ctx, user.OrgID)
		if err != nil {
			return fmt.Errorf("查询用户组织失败: %w", err)
		}
		if org.Status != domain.StatusActive {
			return ErrOrgDisabled
		}
	}
	return nil
}

func toAuthUser(user sqlc.User) AuthUser {
	return AuthUser{
		ID:          uuidToString(user.ID),
		OrgID:       uuidToOptionalString(user.OrgID),
		Username:    user.Username,
		DisplayName: user.DisplayName,
		Role:        user.Role,
		Status:      user.Status,
	}
}
