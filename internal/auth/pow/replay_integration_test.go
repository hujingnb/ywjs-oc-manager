//go:build integration

package pow

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRedisReplayGuard_ConsumeOnce 用真实 Redis 验证一次性消费语义：
// 同一 token 首次 true、二次 false；不同 token 各自首次 true。
func TestRedisReplayGuard_ConsumeOnce(t *testing.T) {
	addr := os.Getenv("INTEGRATION_REDIS_ADDR")
	if addr == "" {
		t.Skip("缺 INTEGRATION_REDIS_ADDR")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	guard := NewRedisReplayGuard(client, "ocm:test:")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	token := "sig-" + time.Now().Format("150405.000")
	first, err := guard.Consume(ctx, token, time.Minute) // 首次消费
	require.NoError(t, err)
	assert.True(t, first)

	second, err := guard.Consume(ctx, token, time.Minute) // 重放
	require.NoError(t, err)
	assert.False(t, second)
}
