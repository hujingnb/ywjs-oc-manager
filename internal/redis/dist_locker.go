package redis

import (
	"context"
	"errors"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// DistLocker 是跨 manager 实例的分布式锁。
// 与 internal/redis/queue.go 同样持"Postgres 是事实来源 / Redis 仅信号通道"哲学:
// 锁失败仅意味着 worker 这一轮放弃,失败重试由 scheduler 兜底。
type DistLocker interface {
	// TryAcquire 用 SET key token NX PX ttl 原子抢锁。
	// 返回 (true, nil) 表示自己拿到;(false, nil) 表示被别人持有。
	TryAcquire(ctx context.Context, key, token string, ttl time.Duration) (bool, error)
	// Renew 校验 token 一致后 PEXPIRE 刷新 TTL;token 不匹配返回 nil 但不动作。
	Renew(ctx context.Context, key, token string, ttl time.Duration) error
	// Refresh 校验 token 一致后 PEXPIRE 续租,返回 (true, nil);
	// token 不匹配(锁已被别人持有或已过期)返回 (false, nil)。
	// 与 Renew 的区别:Refresh 明确告知调用方是否真正续租成功,适用于 leader-election 心跳场景。
	Refresh(ctx context.Context, key, token string, ttl time.Duration) (bool, error)
	// Release 校验 token 一致后 DEL;token 不匹配返回 nil 但不动作(防误删)。
	Release(ctx context.Context, key, token string) error
	// Exists 仅供 follower 在 SUBSCRIBE 后 double-check leader 是否仍在跑。
	Exists(ctx context.Context, key string) (bool, error)
}

// RedisDistLocker 基于 redis SET NX + Lua 实现 DistLocker。
type RedisDistLocker struct {
	client redis.Cmdable
}

// NewRedisDistLocker 创建实例。client 必须支持 Eval。
func NewRedisDistLocker(client redis.Cmdable) *RedisDistLocker {
	return &RedisDistLocker{client: client}
}

// luaRelease:KEYS[1]=lockKey,ARGV[1]=token。
// token 一致才删,防止 leader 已超时丢锁后误删别人新拿到的锁。
const luaRelease = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
end
return 0
`

// luaRenew:同样校验 token 后 PEXPIRE。
const luaRenew = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return 0
`

// TryAcquire 见接口注释。
func (l *RedisDistLocker) TryAcquire(ctx context.Context, key, token string, ttl time.Duration) (bool, error) {
	ok, err := l.client.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return false, err
	}
	return ok, nil
}

// Renew 见接口注释。
func (l *RedisDistLocker) Renew(ctx context.Context, key, token string, ttl time.Duration) error {
	_, err := l.client.Eval(ctx, luaRenew, []string{key}, token, ttl.Milliseconds()).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	return nil
}

// Release 见接口注释。
func (l *RedisDistLocker) Release(ctx context.Context, key, token string) error {
	_, err := l.client.Eval(ctx, luaRelease, []string{key}, token).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	return nil
}

// luaRefresh: KEYS[1]=lockKey, ARGV[1]=token, ARGV[2]=ttlMillis。
// 仅当持有者 token 匹配时 PEXPIRE 续租,返回 1;否则返回 0(已被别人持有或已过期)。
const luaRefresh = `
if redis.call("get", KEYS[1]) == ARGV[1] then
  return redis.call("pexpire", KEYS[1], ARGV[2])
else
  return 0
end`

// Refresh 见接口注释。
func (l *RedisDistLocker) Refresh(ctx context.Context, key, token string, ttl time.Duration) (bool, error) {
	res, err := l.client.Eval(ctx, luaRefresh, []string{key}, token, ttl.Milliseconds()).Int64()
	if err != nil {
		return false, err
	}
	return res == 1, nil
}

// Exists 见接口注释。
func (l *RedisDistLocker) Exists(ctx context.Context, key string) (bool, error) {
	n, err := l.client.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
