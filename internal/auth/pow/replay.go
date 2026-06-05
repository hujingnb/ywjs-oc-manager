package pow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisReplayGuard 用 Redis SETNX 保证同一挑战 signature 只被消费一次，实现防重放。
// 复用 manager 既有 *redis.Client（与 distLocker 共享物理实例）。
type RedisReplayGuard struct {
	client redis.Cmdable // 复用现有 go-redis 客户端
	prefix string        // Redis key 前缀（cfg.Redis.KeyPrefix），隔离共享 Redis 键空间
}

// NewRedisReplayGuard 构造一次性消费守卫。
func NewRedisReplayGuard(client redis.Cmdable, keyPrefix string) *RedisReplayGuard {
	return &RedisReplayGuard{client: client, prefix: keyPrefix}
}

// Consume 尝试消费 token：首次写入返回 true，已存在返回 false（即重放）。
// key 为 prefix+"altcha:used:"+sha256hex(token)，TTL 设为题目剩余有效期，
// 保证一道解最多撑到它本就该过期的时刻且只换一次登录尝试。
func (g *RedisReplayGuard) Consume(ctx context.Context, token string, ttl time.Duration) (bool, error) {
	sum := sha256.Sum256([]byte(token))
	key := g.prefix + "altcha:used:" + hex.EncodeToString(sum[:])
	ok, err := g.client.SetNX(ctx, key, 1, ttl).Result()
	if err != nil {
		return false, err
	}
	return ok, nil
}
