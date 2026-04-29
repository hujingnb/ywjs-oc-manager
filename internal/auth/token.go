package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	TokenTypeAccess  = "access"
	TokenTypeRefresh = "refresh"
)

// Principal 是认证后的用户身份快照。
// 权限判断必须继续在 service 层结合资源归属校验，不能只依赖 token 中的角色。
type Principal struct {
	UserID string `json:"sub"`
	OrgID  string `json:"org_id,omitempty"`
	Role   string `json:"role"`
}

type tokenClaims struct {
	Principal
	Type      string `json:"typ"`
	ExpiresAt int64  `json:"exp"`
	IssuedAt  int64  `json:"iat"`
}

// TokenManager 负责签发和验证 HMAC-SHA256 JWT。
// 这里不引入大型 JWT 依赖，保持签名算法和 claims 结构显式可审计。
type TokenManager struct {
	accessSecret  []byte
	refreshSecret []byte
	accessTTL     time.Duration
	refreshTTL    time.Duration
	now           func() time.Time
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

func (m *TokenManager) sign(principal Principal, tokenType string, ttl time.Duration, secret []byte) (string, error) {
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
