package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// AuthStore 抽象认证流程所需的数据访问能力，便于 service 单元测试使用内存桩。
type AuthStore interface {
	GetUserByUsername(ctx context.Context, username string) (sqlc.User, error)
	GetUserByOrgAndUsername(ctx context.Context, arg sqlc.GetUserByOrgAndUsernameParams) (sqlc.User, error)
	GetUser(ctx context.Context, id pgtype.UUID) (sqlc.User, error)
	MarkUserLoggedIn(ctx context.Context, id pgtype.UUID) (sqlc.User, error)
	GetOrganization(ctx context.Context, id pgtype.UUID) (sqlc.Organization, error)
	GetOrganizationByCode(ctx context.Context, code string) (sqlc.Organization, error)
	CreateRefreshToken(ctx context.Context, arg sqlc.CreateRefreshTokenParams) (sqlc.RefreshToken, error)
	GetRefreshTokenByHash(ctx context.Context, tokenHash string) (sqlc.RefreshToken, error)
	RevokeRefreshToken(ctx context.Context, id pgtype.UUID) (sqlc.RefreshToken, error)
}

// AuthService 处理登录、刷新和注销等认证业务。
type AuthService struct {
	// store 提供用户、组织与 refresh token 的持久化能力。
	store AuthStore
	// tokens 负责签发和校验 access / refresh token。
	tokens *auth.TokenManager
	// passwordHash 在测试中可替换，生产使用 auth.VerifyPassword。
	passwordHash func(string, string) bool
	// now 在测试中可固定时间，确保 refresh token 过期判断可重复。
	now func() time.Time
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

// LoginInput 是用户名密码登录的 service 入参。
// Password 只在内存中参与校验，不会写入日志或返回值。
type LoginInput struct {
	OrgCode  string
	Username string
	Password string
}

// TokenPair 是登录和刷新接口返回的双令牌。
// RefreshToken 只能使用一次，Refresh 成功后旧记录会被撤销。
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// AuthUser 是认证接口暴露的用户快照。
// 该结构不包含 PasswordHash，OrgID 对 platform_admin 为空。
type AuthUser struct {
	ID          string `json:"id"`
	OrgID       string `json:"org_id,omitempty"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Status      string `json:"status"`
}

// LoginResult 聚合当前用户快照和新签发的 token pair。
type LoginResult struct {
	User   AuthUser  `json:"user"`
	Tokens TokenPair `json:"tokens"`
}

// Login 校验用户名密码，签发 access/refresh token，并持久化 refresh token hash。
func (s *AuthService) Login(ctx context.Context, input LoginInput) (LoginResult, error) {
	input.OrgCode = strings.ToLower(strings.TrimSpace(input.OrgCode))
	input.Username = strings.TrimSpace(input.Username)

	user, err := s.lookupLoginUser(ctx, input)
	if err != nil {
		return LoginResult{}, err
	}
	if !s.passwordHash(input.Password, user.PasswordHash) {
		return LoginResult{}, ErrInvalidCredentials
	}
	// 登录前重新检查用户和组织状态，避免已禁用账号继续拿到新令牌。
	if err := s.ensureUserEnabled(ctx, user); err != nil {
		return LoginResult{}, err
	}

	if _, err := s.store.MarkUserLoggedIn(ctx, user.ID); err != nil {
		return LoginResult{}, fmt.Errorf("更新登录时间失败: %w", err)
	}
	return s.issueTokenPair(ctx, user)
}

// lookupLoginUser 根据 org_code 是否为空选择平台登录或组织登录路径。
// 账号不存在、组织标识不存在和角色不匹配统一返回 ErrInvalidCredentials，
// 避免登录接口泄露租户或用户名枚举信息。
func (s *AuthService) lookupLoginUser(ctx context.Context, input LoginInput) (sqlc.User, error) {
	if input.OrgCode == "" {
		user, err := s.store.GetUserByUsername(ctx, input.Username)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return sqlc.User{}, ErrInvalidCredentials
			}
			return sqlc.User{}, fmt.Errorf("查询用户失败: %w", err)
		}
		if user.Role != domain.UserRolePlatformAdmin || user.OrgID.Valid {
			return sqlc.User{}, ErrInvalidCredentials
		}
		return user, nil
	}

	org, err := s.store.GetOrganizationByCode(ctx, input.OrgCode)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.User{}, ErrInvalidCredentials
		}
		return sqlc.User{}, fmt.Errorf("查询组织标识失败: %w", err)
	}
	user, err := s.store.GetUserByOrgAndUsername(ctx, sqlc.GetUserByOrgAndUsernameParams{
		OrgID:    org.ID,
		Username: input.Username,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.User{}, ErrInvalidCredentials
		}
		return sqlc.User{}, fmt.Errorf("查询组织用户失败: %w", err)
	}
	if user.Role == domain.UserRolePlatformAdmin || !user.OrgID.Valid {
		return sqlc.User{}, ErrInvalidCredentials
	}
	return user, nil
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
	// refresh token 采用轮换策略：先撤销旧记录，再签发并持久化新 token pair。
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
	// principal 只放最小授权上下文；角色权限的最终判断仍由 authorizer.go 完成。
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
		UserID: user.ID,
		// 数据库存 hash，明文 refresh token 只返回给客户端一次。
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
