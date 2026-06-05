package service

import (
	"context"
	"log/slog"
	"time"

	"oc-manager/internal/auth/pow"
)

// ReplayGuard 抽象一次性消费能力，便于 CaptchaService 单测注入桩。
// 由 internal/auth/pow.RedisReplayGuard 结构化实现。
type ReplayGuard interface {
	// Consume 首次使用返回 true；已使用返回 false；底层故障返回 err。
	Consume(ctx context.Context, token string, ttl time.Duration) (bool, error)
}

// CaptchaVerifier 抽象登录前置的验证码校验，供 AuthService 注入（nil 表示验证码关闭）。
type CaptchaVerifier interface {
	Verify(ctx context.Context, payload string) error
}

// CaptchaService 编排 PoW 验解与一次性消费，是 CaptchaVerifier 的生产实现，
// 同时为出题接口提供 Challenge。
type CaptchaService struct {
	pow    *pow.Verifier
	replay ReplayGuard
}

// NewCaptchaService 构造验证码服务。
func NewCaptchaService(p *pow.Verifier, r ReplayGuard) *CaptchaService {
	return &CaptchaService{pow: p, replay: r}
}

// Challenge 生成一道挑战，返回值可直接 JSON 序列化给 widget。
func (s *CaptchaService) Challenge() (any, error) {
	return s.pow.CreateChallenge()
}

// Verify 执行登录前置校验：空 payload→Required；验签失败→Invalid；
// 重放→Replayed；一次性消费存储故障→fail-open 放行（仅 Warn 日志）。
func (s *CaptchaService) Verify(ctx context.Context, payload string) error {
	if payload == "" {
		return ErrCaptchaRequired
	}
	sig, err := s.pow.Verify(payload)
	if err != nil {
		return ErrCaptchaInvalid
	}
	firstUse, err := s.replay.Consume(ctx, sig, s.pow.TTL())
	if err != nil {
		// fail-open：Redis 故障时仅保留验签、跳过一次性消费，保登录可用。
		slog.WarnContext(ctx, "验证码一次性消费不可用，fail-open 放行", "error", err)
		return nil
	}
	if !firstUse {
		return ErrCaptchaReplayed
	}
	return nil
}
