package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// AICCUpstreamCircuit 是按上游共享的熔断状态，允许多 manager 副本共用失败窗口和半开探测。
type AICCUpstreamCircuit interface {
	Allow(context.Context, string) (bool, error)
	Record(context.Context, string, bool) error
}

// RedisAICCUpstreamCircuit 将每个 upstream 的连续失败、30 秒滑窗及半开探测持久化到 Redis。
type RedisAICCUpstreamCircuit struct {
	client           redis.Cmdable
	prefix           string
	consecutive      int
	window, cooldown time.Duration
	percent          int
}

func NewRedisAICCUpstreamCircuit(client redis.Cmdable, prefix string, consecutive int, window time.Duration, percent int, cooldown time.Duration) *RedisAICCUpstreamCircuit {
	return &RedisAICCUpstreamCircuit{client: client, prefix: strings.TrimRight(prefix, ":"), consecutive: consecutive, window: window, percent: percent, cooldown: cooldown}
}
func (c *RedisAICCUpstreamCircuit) key(upstream, suffix string) string {
	return c.prefix + ":aicc:circuit:" + upstream + ":" + suffix
}

const aiccCircuitRecordLua = `
local now=tonumber(ARGV[1]); local window=tonumber(ARGV[2]); local threshold=tonumber(ARGV[3]); local pct=tonumber(ARGV[4]); local cooldown=tonumber(ARGV[5]); local overload=ARGV[6]
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now-window); redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now-window)
redis.call('ZADD', KEYS[1], now, ARGV[7]); if overload=='1' then redis.call('ZADD', KEYS[2], now, ARGV[7]); redis.call('HINCRBY', KEYS[3], 'consecutive', 1) else redis.call('HSET', KEYS[3], 'consecutive', 0) end
local total=redis.call('ZCARD', KEYS[1]); local failed=redis.call('ZCARD', KEYS[2]); local cons=tonumber(redis.call('HGET', KEYS[3], 'consecutive') or '0')
if cons>=threshold or (total>0 and failed*100>=total*pct) then redis.call('HSET', KEYS[3], 'open_until', now+cooldown); redis.call('DEL', KEYS[4]) end
redis.call('PEXPIRE', KEYS[1], window*2); redis.call('PEXPIRE', KEYS[2], window*2); redis.call('PEXPIRE', KEYS[3], cooldown*2); return 1`

func (c *RedisAICCUpstreamCircuit) Allow(ctx context.Context, upstream string) (bool, error) {
	if c == nil || c.client == nil {
		return false, fmt.Errorf("aicc circuit 未配置")
	}
	now := time.Now().UnixMilli()
	state := c.key(upstream, "state")
	until, err := c.client.HGet(ctx, state, "open_until").Int64()
	if err != nil && err != redis.Nil {
		return false, err
	}
	if until == 0 {
		return true, nil
	}
	if now < until {
		return false, nil
	}
	ok, err := c.client.SetNX(ctx, c.key(upstream, "probe"), "1", c.cooldown).Result()
	return ok, err
}
func (c *RedisAICCUpstreamCircuit) Record(ctx context.Context, upstream string, overload bool) error {
	now := time.Now().UnixMilli()
	id := fmt.Sprintf("%d-%d", now, time.Now().UnixNano())
	_, err := c.client.Eval(ctx, aiccCircuitRecordLua, []string{c.key(upstream, "outcomes"), c.key(upstream, "failures"), c.key(upstream, "state"), c.key(upstream, "probe")}, now, c.window.Milliseconds(), c.consecutive, c.percent, c.cooldown.Milliseconds(), boolToInt(overload), id).Result()
	return err
}
func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
