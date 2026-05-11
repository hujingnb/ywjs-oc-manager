// Package auth 提供 manager 登录认证、密码哈希、令牌签发与敏感字段加密等安全原语。
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// TokenTypeAccess 标记短期访问令牌，只能用 access secret 校验。
	TokenTypeAccess = "access"
	// TokenTypeRefresh 标记续期令牌，只能用 refresh secret 校验并入库保存 hash。
	TokenTypeRefresh = "refresh"
)

// Principal 是认证后的用户身份快照。
// 权限判断必须继续在 service 层结合资源归属校验，不能只依赖 token 中的角色。
type Principal struct {
	// UserID 是认证主体的用户 ID，作为审计和“本人”权限判断的身份来源。
	UserID string `json:"sub"`
	// OrgID 是用户所属组织；平台管理员可为空，组织角色必须结合资源组织再次校验。
	OrgID string `json:"org_id,omitempty"`
	// Role 是令牌签发时的角色快照，权限谓词仍需结合资源归属防止越权。
	Role string `json:"role"`
}

type tokenClaims struct {
	Principal
	// Type 区分 access/refresh，防止 refresh token 被误用到 API 访问路径。
	Type      string `json:"typ"`
	ExpiresAt int64  `json:"exp"`
	IssuedAt  int64  `json:"iat"`
}

// TokenManager 负责签发和验证 HMAC-SHA256 JWT。
// 这里不引入大型 JWT 依赖，保持签名算法和 claims 结构显式可审计。
type TokenManager struct {
	// accessSecret 只签发短期 access token，不能与 refreshSecret 共用。
	accessSecret []byte
	// refreshSecret 只签发 refresh token，泄露影响面必须与 access token 隔离。
	refreshSecret []byte
	// accessTTL 控制 API 访问令牌有效期，通常短于 refreshTTL。
	accessTTL time.Duration
	// refreshTTL 控制续期令牌有效期，service 层会用它计算持久化过期时间。
	refreshTTL time.Duration
	// now 允许测试固定时间，生产路径使用 time.Now。
	now func() time.Time
}

// NewTokenManager 创建令牌管理器。
func NewTokenManager(accessSecret, refreshSecret string, accessTTL, refreshTTL time.Duration) (*TokenManager, error) {
	if accessSecret == "" || refreshSecret == "" {
		return nil, errors.New("JWT secret 不能为空")
	}
	if accessTTL <= 0 || refreshTTL <= 0 {
		return nil, errors.New("token TTL 必须大于 0")
	}
	return &TokenManager{
		accessSecret:  []byte(accessSecret),
		refreshSecret: []byte(refreshSecret),
		accessTTL:     accessTTL,
		refreshTTL:    refreshTTL,
		now:           time.Now,
	}, nil
}

// SignAccessToken 签发短期 access token。
func (m *TokenManager) SignAccessToken(principal Principal) (string, error) {
	return m.sign(principal, TokenTypeAccess, m.accessTTL, m.accessSecret)
}

// SignRefreshToken 签发长期 refresh token。
func (m *TokenManager) SignRefreshToken(principal Principal) (string, error) {
	return m.sign(principal, TokenTypeRefresh, m.refreshTTL, m.refreshSecret)
}

// VerifyAccessToken 校验 access token 并返回认证主体。
func (m *TokenManager) VerifyAccessToken(token string) (Principal, error) {
	return m.verify(token, TokenTypeAccess, m.accessSecret)
}

// VerifyRefreshToken 校验 refresh token 并返回认证主体。
func (m *TokenManager) VerifyRefreshToken(token string) (Principal, error) {
	return m.verify(token, TokenTypeRefresh, m.refreshSecret)
}

// RefreshTTL 返回 refresh token 有效期，供 service 持久化过期时间。
func (m *TokenManager) RefreshTTL() time.Duration {
	return m.refreshTTL
}

func (m *TokenManager) sign(principal Principal, tokenType string, ttl time.Duration, secret []byte) (string, error) {
	// token 中必须至少包含用户和角色；资源组织权限仍由 authorizer 在业务层二次校验。
	if principal.UserID == "" || principal.Role == "" {
		return "", errors.New("token principal 不完整")
	}
	now := m.now().UTC()
	claims := tokenClaims{
		Principal: principal,
		Type:      tokenType,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(ttl).Unix(),
	}

	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}

	unsigned := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	signature := signHMAC(unsigned, secret)
	return unsigned + "." + signature, nil
}

func (m *TokenManager) verify(token string, expectedType string, secret []byte) (Principal, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Principal{}, errors.New("token 格式错误")
	}

	// 先验签再解析 claims，避免处理攻击者伪造的 payload。
	expectedSignature := signHMAC(parts[0]+"."+parts[1], secret)
	if !hmac.Equal([]byte(expectedSignature), []byte(parts[2])) {
		return Principal{}, errors.New("token 签名无效")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Principal{}, fmt.Errorf("解析 token payload 失败: %w", err)
	}
	var claims tokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Principal{}, fmt.Errorf("解析 token claims 失败: %w", err)
	}
	if claims.Type != expectedType {
		return Principal{}, errors.New("token 类型不匹配")
	}
	if m.now().UTC().Unix() >= claims.ExpiresAt {
		return Principal{}, errors.New("token 已过期")
	}
	if claims.UserID == "" || claims.Role == "" {
		return Principal{}, errors.New("token principal 不完整")
	}
	return claims.Principal, nil
}

func signHMAC(unsigned string, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(unsigned))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// HashOpaqueToken 对 refresh token 做不可逆 hash 后再入库。
// 即使数据库泄露，攻击者也不能直接拿库里的值调用刷新接口。
func HashOpaqueToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
