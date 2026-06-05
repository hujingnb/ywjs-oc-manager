package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth/pow"
)

// fakeReplay 是 ReplayGuard 的可控桩：firstUse 决定首次/重放，err 模拟 Redis 故障。
type fakeReplay struct {
	firstUse bool
	err      error
	calls    int
}

func (f *fakeReplay) Consume(_ context.Context, _ string, _ time.Duration) (bool, error) {
	f.calls++
	return f.firstUse, f.err
}

// buildAltchaPayload 按 widget 提交格式拼 base64(JSON) payload。
func buildAltchaPayload(algorithm, challenge, salt, signature string, number int64) string {
	b, _ := json.Marshal(map[string]any{
		"algorithm": algorithm,
		"challenge": challenge,
		"number":    number,
		"salt":      salt,
		"signature": signature,
	})
	return base64.StdEncoding.EncodeToString(b)
}

// solveValidPayload 用 altcha 文档算法暴力解出有效 payload，供 CaptchaService 用例。
func solveValidPayload(t *testing.T, v *pow.Verifier) string {
	t.Helper()
	ch, err := v.CreateChallenge()
	require.NoError(t, err)
	for n := int64(0); n <= ch.MaxNumber; n++ {
		sum := sha256.Sum256([]byte(ch.Salt + strconv.FormatInt(n, 10)))
		if hex.EncodeToString(sum[:]) == ch.Challenge {
			return buildAltchaPayload(ch.Algorithm, ch.Challenge, ch.Salt, ch.Signature, n)
		}
	}
	t.Fatal("未找到解")
	return ""
}

// 缺 payload → ErrCaptchaRequired。
func TestCaptchaServiceRequiresPayload(t *testing.T) {
	v := pow.NewVerifier("s", 5000, time.Minute)
	svc := NewCaptchaService(v, &fakeReplay{firstUse: true})
	err := svc.Verify(context.Background(), "")
	require.ErrorIs(t, err, ErrCaptchaRequired)
}

// 无效 payload → ErrCaptchaInvalid。
func TestCaptchaServiceRejectsInvalid(t *testing.T) {
	v := pow.NewVerifier("s", 5000, time.Minute)
	svc := NewCaptchaService(v, &fakeReplay{firstUse: true})
	err := svc.Verify(context.Background(), "garbage")
	require.ErrorIs(t, err, ErrCaptchaInvalid)
}

// 有效 payload 但已被消费 → ErrCaptchaReplayed。
func TestCaptchaServiceRejectsReplay(t *testing.T) {
	v := pow.NewVerifier("s", 5000, time.Minute)
	svc := NewCaptchaService(v, &fakeReplay{firstUse: false}) // 模拟已消费
	err := svc.Verify(context.Background(), solveValidPayload(t, v))
	require.ErrorIs(t, err, ErrCaptchaReplayed)
}

// 有效 payload + Redis 故障 → fail-open 放行（返回 nil）。
func TestCaptchaServiceFailOpenOnReplayError(t *testing.T) {
	v := pow.NewVerifier("s", 5000, time.Minute)
	svc := NewCaptchaService(v, &fakeReplay{err: errors.New("redis down")})
	err := svc.Verify(context.Background(), solveValidPayload(t, v))
	require.NoError(t, err)
}

// 有效 payload + 首次消费 → 通过。
func TestCaptchaServiceAcceptsFirstUse(t *testing.T) {
	v := pow.NewVerifier("s", 5000, time.Minute)
	svc := NewCaptchaService(v, &fakeReplay{firstUse: true})
	err := svc.Verify(context.Background(), solveValidPayload(t, v))
	require.NoError(t, err)
}
