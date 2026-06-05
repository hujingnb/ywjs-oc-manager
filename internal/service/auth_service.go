package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// AuthStore 抽象认证流程所需的数据访问能力，便于 service 单元测试使用内存桩。
type AuthStore interface {
	GetUserByUsername(ctx context.Context, username string) (sqlc.User, error)
	GetUserByOrgAndUsername(ctx context.Context, arg sqlc.GetUserByOrgAndUsernameParams) (sqlc.User, error)
	GetUser(ctx context.Context, id string) (sqlc.User, error)
	UpdateUserPassword(ctx context.Context, arg sqlc.UpdateUserPasswordParams) error
	MarkUserLoggedIn(ctx context.Context, id string) error
	GetOrganization(ctx context.Context, id string) (sqlc.Organization, error)
	GetOrganizationByCode(ctx context.Context, code string) (sqlc.Organization, error)
	// CreateRefreshToken 写入 refresh token（:exec），service 写入后通过 GetRefreshTokenByHash 读回。
	CreateRefreshToken(ctx context.Context, arg sqlc.CreateRefreshTokenParams) error
	GetRefreshTokenByHash(ctx context.Context, tokenHash string) (sqlc.RefreshToken, error)
	RevokeRefreshToken(ctx context.Context, id string) error
	RevokeRefreshTokensByUser(ctx context.Context, userID string) error
}

// AuthService 处理登录、刷新和注销等认证业务。
type AuthService struct {
	// store 提供用户、组织与 refresh token 的持久化能力。
	store AuthStore
	// tokens 负责签发和校验 access / refresh token。
	tokens *auth.TokenManager
	// captcha 为验证码前置校验器；nil 表示验证码关闭，Login 直接跳过。
	captcha CaptchaVerifier
	// verifyPassword 在测试中可替换，生产使用 auth.VerifyPassword 校验 PHC hash。
	verifyPassword func(string, string) bool
	// hashPassword 在修改密码时生成 PHC hash，测试可替换为确定性快路径。
	hashPassword PasswordHasher
	// now 在测试中可固定时间，确保 refresh token 过期判断可重复。
	now func() time.Time
}

// NewAuthService 创建认证服务。captcha 为 nil 时不启用登录验证码校验。
func NewAuthService(store AuthStore, tokens *auth.TokenManager, captcha CaptchaVerifier) *AuthService {
	return &AuthService{
		store:          store,
		tokens:         tokens,
		captcha:        captcha,
		verifyPassword: auth.VerifyPassword,
		hashPassword: func(password string) (string, error) {
			return auth.HashPassword(password, auth.DefaultPasswordParams)
		},
		now: time.Now,
	}
}

// LoginInput 是用户名密码登录的 service 入参。
// Password 只在内存中参与校验，不会写入日志或返回值。
type LoginInput struct {
	OrgCode  string
	Username string
	Password string
	// Captcha 是 Altcha payload（base64）；验证码开启时由 Login 前置校验，关闭时忽略。
	Captcha string
}

// ChangePasswordInput 是已登录用户自助修改密码的 service 入参。
type ChangePasswordInput struct {
	// OldPassword 是当前密码，用于证明调用方仍持有账号凭据。
	OldPassword string
	// NewPassword 是待写入的新密码明文，service 校验后只保存 hash。
	NewPassword string
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
	// 验证码前置校验：开启时（captcha != nil）必须先过 PoW + 一次性消费，
	// 失败直接返回，连密码校验（Argon2id，开销大）都不触发。
	if s.captcha != nil {
		if err := s.captcha.Verify(ctx, input.Captcha); err != nil {
			return LoginResult{}, err
		}
	}
	input.OrgCode = strings.ToLower(strings.TrimSpace(input.OrgCode))
	input.Username = strings.TrimSpace(input.Username)

	user, err := s.lookupLoginUser(ctx, input)
	if err != nil {
		return LoginResult{}, err
	}
	if !s.verifyPassword(input.Password, user.PasswordHash) {
		return LoginResult{}, ErrInvalidCredentials
	}
	// 登录前重新检查用户和组织状态，避免已禁用账号继续拿到新令牌。
	if err := s.ensureUserEnabled(ctx, user); err != nil {
		return LoginResult{}, err
	}

	if err := s.store.MarkUserLoggedIn(ctx, user.ID); err != nil {
		return LoginResult{}, fmt.Errorf("更新登录时间失败: %w", err)
	}
	return s.issueTokenPair(ctx, user)
}

// lookupLoginUser 根据 org_code 是否为空选择平台登录或组织登录路径。
// 账号不存在、企业标识不存在和角色不匹配统一返回 ErrInvalidCredentials，
// 避免登录接口泄露租户或用户名枚举信息。
func (s *AuthService) lookupLoginUser(ctx context.Context, input LoginInput) (sqlc.User, error) {
	if input.OrgCode == "" {
		user, err := s.store.GetUserByUsername(ctx, input.Username)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
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
		if errors.Is(err, sql.ErrNoRows) {
			return sqlc.User{}, ErrInvalidCredentials
		}
		return sqlc.User{}, fmt.Errorf("查询企业标识失败: %w", err)
	}
	user, err := s.store.GetUserByOrgAndUsername(ctx, sqlc.GetUserByOrgAndUsernameParams{
		OrgID:    null.StringFrom(org.ID),
		Username: input.Username,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sqlc.User{}, ErrInvalidCredentials
		}
		return sqlc.User{}, fmt.Errorf("查询企业用户失败: %w", err)
	}
	if user.Role == domain.UserRolePlatformAdmin || !user.OrgID.Valid {
		return sqlc.User{}, ErrInvalidCredentials
	}
	return user, nil
}

// Me 根据 access token 中的主体加载最新用户状态。
func (s *AuthService) Me(ctx context.Context, principal auth.Principal) (AuthUser, error) {
	user, err := s.store.GetUser(ctx, principal.UserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AuthUser{}, ErrInvalidToken
		}
		return AuthUser{}, fmt.Errorf("查询当前用户失败: %w", err)
	}
	if err := s.ensureUserEnabled(ctx, user); err != nil {
		return AuthUser{}, err
	}
	return toAuthUser(user), nil
}

// ChangePassword 允许已登录用户在验证旧密码后修改自己的 manager 登录密码。
func (s *AuthService) ChangePassword(ctx context.Context, principal auth.Principal, input ChangePasswordInput) error {
	user, err := s.store.GetUser(ctx, principal.UserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidToken
		}
		return fmt.Errorf("查询当前用户失败: %w", err)
	}
	// 修改密码同样要求当前用户和所属企业仍处于 active 状态，避免禁用账号绕过登录限制。
	if err := s.ensureUserEnabled(ctx, user); err != nil {
		return err
	}
	if !s.verifyPassword(input.OldPassword, user.PasswordHash) {
		return ErrInvalidCredentials
	}
	// 新密码规则保持在认证服务内，管理员重置密码不复用此流程。
	if input.NewPassword == "" {
		return fmt.Errorf("%w: 新密码不能为空", ErrMemberCreateInvalid)
	}
	if len(input.NewPassword) < 8 {
		return fmt.Errorf("%w: 新密码至少 8 位", ErrMemberCreateInvalid)
	}
	if input.NewPassword == input.OldPassword {
		return fmt.Errorf("%w: 新密码不能与当前密码相同", ErrMemberCreateInvalid)
	}
	hashed, err := s.hashPassword(input.NewPassword)
	if err != nil {
		return fmt.Errorf("生成密码 hash 失败: %w", err)
	}
	// UpdateUserPassword 只写 password_hash，避免自助改密影响账号资料、角色或状态字段。
	if err := s.store.UpdateUserPassword(ctx, sqlc.UpdateUserPasswordParams{ID: user.ID, PasswordHash: hashed}); err != nil {
		return fmt.Errorf("更新当前用户密码失败: %w", err)
	}
	// 改密成功后撤销当前用户所有 refresh token，防止旧设备或泄露 token 继续换取 access token。
	if err := s.store.RevokeRefreshTokensByUser(ctx, user.ID); err != nil {
		return fmt.Errorf("撤销当前用户 refresh token 失败: %w", err)
	}
	return nil
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
	// RevokedAt / ExpiresAt 现为 null.Time；valid+not-expired 才允许刷新。
	if record.RevokedAt.Valid || !record.ExpiresAt.After(s.now()) {
		return LoginResult{}, ErrInvalidToken
	}

	user, err := s.store.GetUser(ctx, principal.UserID)
	if err != nil {
		return LoginResult{}, fmt.Errorf("查询刷新用户失败: %w", err)
	}
	if err := s.ensureUserEnabled(ctx, user); err != nil {
		return LoginResult{}, err
	}
	// refresh token 采用轮换策略：先撤销旧记录，再签发并持久化新 token pair。
	if err := s.store.RevokeRefreshToken(ctx, record.ID); err != nil {
		return LoginResult{}, fmt.Errorf("撤销旧 refresh token 失败: %w", err)
	}
	return s.issueTokenPair(ctx, user)
}

// Logout 撤销 refresh token；重复注销或 token 不存在按成功处理，保证接口幂等。
func (s *AuthService) Logout(ctx context.Context, refreshToken string) error {
	record, err := s.store.GetRefreshTokenByHash(ctx, auth.HashOpaqueToken(refreshToken))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("查询 refresh token 失败: %w", err)
	}
	if record.RevokedAt.Valid {
		return nil
	}
	if err := s.store.RevokeRefreshToken(ctx, record.ID); err != nil {
		return fmt.Errorf("撤销 refresh token 失败: %w", err)
	}
	return nil
}

func (s *AuthService) issueTokenPair(ctx context.Context, user sqlc.User) (LoginResult, error) {
	// principal 只放最小授权上下文；角色权限的最终判断仍由 authorizer.go 完成。
	principal := auth.Principal{
		UserID: user.ID,
		OrgID:  strOrEmpty(user.OrgID),
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
	// CreateRefreshToken 为 :exec；数据库存 hash，明文 refresh token 只返回给客户端一次。
	// ExpiresAt 现为 time.Time（MySQL DATETIME）。
	if err := s.store.CreateRefreshToken(ctx, sqlc.CreateRefreshTokenParams{
		ID:        newUUID(),
		UserID:    user.ID,
		TokenHash: auth.HashOpaqueToken(refreshToken),
		ExpiresAt: s.now().Add(s.tokens.RefreshTTL()),
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
		org, err := s.store.GetOrganization(ctx, user.OrgID.String)
		if err != nil {
			return fmt.Errorf("查询用户所属企业失败: %w", err)
		}
		if org.Status != domain.StatusActive {
			return ErrOrgDisabled
		}
	}
	return nil
}

func toAuthUser(user sqlc.User) AuthUser {
	return AuthUser{
		ID:          user.ID,
		OrgID:       strOrEmpty(user.OrgID),
		Username:    user.Username,
		DisplayName: user.DisplayName,
		Role:        user.Role,
		Status:      user.Status,
	}
}
