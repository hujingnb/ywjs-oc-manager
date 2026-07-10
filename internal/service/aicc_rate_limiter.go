package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// AICCRateLimiter 是 AICC 公开匿名入口的限流抽象。
type AICCRateLimiter interface {
	// Allow 返回当前 key 在窗口期内是否仍允许继续执行。
	Allow(ctx context.Context, key string, limit int64, window time.Duration) (bool, error)
}

// RedisAICCRateLimiter 使用 Redis INCR + EXPIRE 实现固定窗口限流。
type RedisAICCRateLimiter struct {
	client redis.Cmdable
	prefix string
}

// NewRedisAICCRateLimiter 创建 AICC 公开入口 Redis 限流器。
func NewRedisAICCRateLimiter(client redis.Cmdable, prefix string) *RedisAICCRateLimiter {
	return &RedisAICCRateLimiter{client: client, prefix: prefix}
}

// Allow 判断当前 key 是否低于固定窗口上限。
func (l *RedisAICCRateLimiter) Allow(ctx context.Context, key string, limit int64, window time.Duration) (bool, error) {
	if l == nil || l.client == nil || limit <= 0 || window <= 0 {
		return true, nil
	}
	fullKey := strings.TrimRight(l.prefix, ":") + ":aicc:ratelimit:" + strings.TrimSpace(key)
	count, err := l.client.Incr(ctx, fullKey).Result()
	if err != nil {
		return false, fmt.Errorf("AICC 限流计数失败: %w", err)
	}
	if count == 1 {
		if err := l.client.Expire(ctx, fullKey, window).Err(); err != nil {
			return false, fmt.Errorf("AICC 限流过期时间设置失败: %w", err)
		}
	}
	return count <= limit, nil
}
