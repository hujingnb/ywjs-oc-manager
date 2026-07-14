package service

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// TestRedisAICCHierarchicalLimiterRenewsAndProtectsNewHolder 验证长任务续租后仍占额，旧 holder 的重复 release 不会扣减新 holder。
func TestRedisAICCHierarchicalLimiterRenewsAndProtectsNewHolder(t *testing.T) {
	mr := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer clientA.Close()
	defer clientB.Close()
	limits := AICCConcurrencyLimits{Upstream: 1, Org: 1, Agent: 1, Session: 1}
	limiterA := NewRedisAICCHierarchicalLimiter(clientA, "test:", limits)
	limiterB := NewRedisAICCHierarchicalLimiter(clientB, "test:", limits)
	limiterA.ttl = 30 * time.Millisecond
	limiterB.ttl = 30 * time.Millisecond
	releaseA, err := limiterA.Acquire(context.Background(), "org", "agent", "session")
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)
	_, err = limiterB.Acquire(context.Background(), "org", "agent", "session")
	require.ErrorIs(t, err, ErrAICCConcurrencyLimited)
	releaseA()
	releaseB, err := limiterB.Acquire(context.Background(), "org", "agent", "session")
	require.NoError(t, err)
	// releaseA 的 once 与 token 校验都不能影响已由 B 获得的新租约。
	releaseA()
	_, err = limiterA.Acquire(context.Background(), "org", "agent", "session")
	require.ErrorIs(t, err, ErrAICCConcurrencyLimited)
	releaseB()
}
