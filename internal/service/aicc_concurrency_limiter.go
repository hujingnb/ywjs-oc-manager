package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// AICCConcurrencyLimits 是四层同时执行额度；每层都由 Redis Lua 原子占用，所有 manager 副本共享。
type AICCConcurrencyLimits struct{ Upstream, Org, Agent, Session int64 }

// RedisAICCHierarchicalLimiter 使用带 owner token 的租约集合实现跨副本层级并发保护。
type RedisAICCHierarchicalLimiter struct {
	client redis.Cmdable
	prefix string
	limits AICCConcurrencyLimits
	ttl    time.Duration
}

// NewRedisAICCHierarchicalLimiter 创建生产 limiter；ttl 仅用于进程崩溃后的泄漏自愈，正常路径必须显式释放。
func NewRedisAICCHierarchicalLimiter(client redis.Cmdable, prefix string, limits AICCConcurrencyLimits) *RedisAICCHierarchicalLimiter {
	return &RedisAICCHierarchicalLimiter{client: client, prefix: strings.TrimRight(prefix, ":"), limits: limits, ttl: time.Minute}
}

const aiccAcquireHierarchyLua = `
local now=tonumber(ARGV[#ARGV-1]); local expires=tonumber(ARGV[#ARGV]);
for i=1,#KEYS do redis.call('ZREMRANGEBYSCORE', KEYS[i], '-inf', now); if redis.call('ZCARD', KEYS[i]) >= tonumber(ARGV[i]) then return 0 end end
for i=1,#KEYS do redis.call('ZADD', KEYS[i], expires, ARGV[#ARGV-2]); redis.call('PEXPIRE', KEYS[i], expires-now) end
return 1`
const aiccReleaseHierarchyLua = `
for i=1,#KEYS do redis.call('ZREM', KEYS[i], ARGV[1]) end
return 1`
const aiccRenewHierarchyLua = `
local now=tonumber(ARGV[2]); local expires=tonumber(ARGV[3]);
for i=1,#KEYS do redis.call('ZREMRANGEBYSCORE', KEYS[i], '-inf', now); if redis.call('ZSCORE', KEYS[i], ARGV[1]) == false then return 0 end end
for i=1,#KEYS do redis.call('ZADD', KEYS[i], expires, ARGV[1]); redis.call('PEXPIRE', KEYS[i], expires-now) end
return 1`

// Acquire 原子占用 upstream、组织、智能体和会话四个 scope；任一层已满均不改变任何计数。
func (l *RedisAICCHierarchicalLimiter) Acquire(ctx context.Context, orgID, agentID, sessionID string) (func(), error) {
	if l == nil || l.client == nil {
		return nil, fmt.Errorf("%w: limiter 未配置", ErrAICCConcurrencyLimited)
	}
	keys := []string{l.prefix + ":aicc:concurrency:upstream:hermes", l.prefix + ":aicc:concurrency:org:" + orgID, l.prefix + ":aicc:concurrency:agent:" + agentID, l.prefix + ":aicc:concurrency:session:" + sessionID}
	token := newUUID()
	now := time.Now()
	expires := now.Add(l.ttl)
	res, err := l.client.Eval(ctx, aiccAcquireHierarchyLua, keys, l.limits.Upstream, l.limits.Org, l.limits.Agent, l.limits.Session, token, now.UnixMilli(), expires.UnixMilli()).Int64()
	if err != nil {
		return nil, fmt.Errorf("aicc 并发额度存储失败: %w", err)
	}
	if res != 1 {
		return nil, ErrAICCConcurrencyLimited
	}
	done := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(l.ttl / 3)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				now := time.Now()
				_, _ = l.client.Eval(context.Background(), aiccRenewHierarchyLua, keys, token, now.UnixMilli(), now.Add(l.ttl).UnixMilli()).Result()
			}
		}
	}()
	return func() {
		once.Do(func() {
			close(done)
			_, _ = l.client.Eval(context.Background(), aiccReleaseHierarchyLua, keys, token).Result()
		})
	}, nil
}
