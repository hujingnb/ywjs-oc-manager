// Package pow 封装 Altcha 工作量证明的出题与验解，是全仓库唯一接触
// altcha-lib-go 的位置。对外暴露与 altcha 解耦的稳定契约：出题返回可直接
// JSON 序列化给 widget 的挑战；验解返回该题 signature（供上层做一次性消费 key）。
package pow

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go"
)

// ErrInvalidSolution 表示 payload 验签失败、解不成立或已过期，统一由上层映射为 400。
var ErrInvalidSolution = errors.New("altcha 解校验失败")

// Verifier 持有出题/验签所需的 HMAC 密钥、难度上限与有效期。
type Verifier struct {
	hmacKey   string        // HMAC 签名密钥（captcha.hmac_secret）
	maxNumber int64         // 难度上限（captcha.difficulty），常驻取低值≈几百 ms
	ttl       time.Duration // 挑战有效期（captcha.ttl），也是一次性 key 的最长 TTL
}

// NewVerifier 构造 Verifier。
func NewVerifier(hmacKey string, maxNumber int64, ttl time.Duration) *Verifier {
	return &Verifier{hmacKey: hmacKey, maxNumber: maxNumber, ttl: ttl}
}

// TTL 返回挑战有效期，供一次性消费设置 Redis key 过期时间。
func (v *Verifier) TTL() time.Duration { return v.ttl }

// CreateChallenge 生成一道带 HMAC 签名与过期时间的挑战；服务端无需保存任何状态。
// 返回的 altcha.Challenge 带 json tag（algorithm/challenge/maxNumber/salt/signature），
// handler 直接 c.JSON 即为 widget 需要的形态。
func (v *Verifier) CreateChallenge() (altcha.Challenge, error) {
	expires := time.Now().Add(v.ttl)
	return altcha.CreateChallenge(altcha.ChallengeOptions{
		HMACKey:   v.hmacKey,
		MaxNumber: v.maxNumber,
		Expires:   &expires,
	})
}

// Verify 校验 base64 payload：验 HMAC 签名 + 重算解 + 未过期（checkExpires=true）。
// 成功返回该题 signature（一次性消费 key 的来源）；任何失败返回 ErrInvalidSolution。
// signature 自行从 payload 解析，避免依赖 altcha 内部 payload 表示。
func (v *Verifier) Verify(payloadB64 string) (string, error) {
	ok, err := altcha.VerifySolution(payloadB64, v.hmacKey, true)
	if err != nil || !ok {
		return "", ErrInvalidSolution
	}
	raw, err := base64.StdEncoding.DecodeString(payloadB64)
	if err != nil {
		return "", ErrInvalidSolution
	}
	var p struct {
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.Signature == "" {
		return "", ErrInvalidSolution
	}
	return p.Signature, nil
}
