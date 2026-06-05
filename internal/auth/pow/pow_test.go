package pow

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// solveChallenge 用 altcha 文档算法暴力找 number：hex(sha256(salt+number)) == challenge。
// 仅测试使用，保证不依赖库的 solver 符号。
func solveChallenge(t *testing.T, salt, challenge string, maxNumber int64) int64 {
	t.Helper()
	for n := int64(0); n <= maxNumber; n++ {
		sum := sha256.Sum256([]byte(salt + strconv.FormatInt(n, 10)))
		if hex.EncodeToString(sum[:]) == challenge {
			return n
		}
	}
	t.Fatalf("在 maxNumber=%d 内未找到解，疑似 altcha 摘要算法与测试不一致", maxNumber)
	return 0
}

// buildPayload 按 widget 提交格式拼 base64(JSON) payload。
func buildPayload(algorithm, challenge, salt, signature string, number int64) string {
	b, _ := json.Marshal(map[string]any{
		"algorithm": algorithm,
		"challenge": challenge,
		"number":    number,
		"salt":      salt,
		"signature": signature,
	})
	return base64.StdEncoding.EncodeToString(b)
}

// 正常路径：有效解通过校验，并返回该题 signature 作为一次性 key。
func TestVerifierAcceptsValidSolution(t *testing.T) {
	v := NewVerifier("test-secret", 5000, time.Minute)
	ch, err := v.CreateChallenge()
	require.NoError(t, err)
	n := solveChallenge(t, ch.Salt, ch.Challenge, ch.MaxNumber)
	payload := buildPayload(ch.Algorithm, ch.Challenge, ch.Salt, ch.Signature, n)

	sig, err := v.Verify(payload)
	require.NoError(t, err)
	assert.Equal(t, ch.Signature, sig) // 返回的 signature 即出题时的 signature
}

// 异常路径：篡改 number 后重算不成立，校验失败。
func TestVerifierRejectsTamperedSolution(t *testing.T) {
	v := NewVerifier("test-secret", 5000, time.Minute)
	ch, err := v.CreateChallenge()
	require.NoError(t, err)
	n := solveChallenge(t, ch.Salt, ch.Challenge, ch.MaxNumber)
	payload := buildPayload(ch.Algorithm, ch.Challenge, ch.Salt, ch.Signature, n+1) // 改坏 number

	_, err = v.Verify(payload)
	require.ErrorIs(t, err, ErrInvalidSolution)
}

// 边界路径：挑战已过期（ttl 取负把 Expires 推到过去）→ 校验失败。
func TestVerifierRejectsExpiredChallenge(t *testing.T) {
	v := NewVerifier("test-secret", 5000, -time.Minute)
	ch, err := v.CreateChallenge()
	require.NoError(t, err)
	n := solveChallenge(t, ch.Salt, ch.Challenge, ch.MaxNumber)
	payload := buildPayload(ch.Algorithm, ch.Challenge, ch.Salt, ch.Signature, n)

	_, err = v.Verify(payload)
	require.ErrorIs(t, err, ErrInvalidSolution)
}

// 异常路径：非法 base64 直接判失败，不 panic。
func TestVerifierRejectsGarbage(t *testing.T) {
	v := NewVerifier("test-secret", 5000, time.Minute)
	_, err := v.Verify("!!!not-base64!!!")
	require.ErrorIs(t, err, ErrInvalidSolution)
}
