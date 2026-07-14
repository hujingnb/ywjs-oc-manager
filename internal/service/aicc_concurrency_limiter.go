package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// AICCConcurrencyLimits 是四层同时执行额度；每层都由 Redis Lua 原子占用，所有 manager 副本共享。
type AICCConcurrencyLimits struct{ Upstream, Org, Agent, Session int64 }

// RedisAICCHierarchicalLimiter 使用计数器实现跨副本层级并发保护；release 由 dispatcher defer 保证。
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
for i=1,#KEYS do if tonumber(redis.call('GET', KEYS[i]) or '0') >= tonumber(ARGV[i]) then return 0 end end
for i=1,#KEYS do redis.call('INCR', KEYS[i]); redis.call('PEXPIRE', KEYS[i], ARGV[#KEYS+1]) end
return 1`
const aiccReleaseHierarchyLua = `
for i=1,#KEYS do local n=redis.call('DECR', KEYS[i]); if n<=0 then redis.call('DEL', KEYS[i]) end end
return 1`

// Acquire 原子占用 upstream、组织、智能体和会话四个 scope；任一层已满均不改变任何计数。
func (l *RedisAICCHierarchicalLimiter) Acquire(ctx context.Context, orgID, agentID, sessionID string) (func(), error) {
	if l == nil || l.client == nil {
		return nil, fmt.Errorf("%w: limiter 未配置", ErrAICCConcurrencyLimited)
	}
	keys := []string{l.prefix + ":aicc:concurrency:upstream:hermes", l.prefix + ":aicc:concurrency:org:" + orgID, l.prefix + ":aicc:concurrency:agent:" + agentID, l.prefix + ":aicc:concurrency:session:" + sessionID}
	res, err := l.client.Eval(ctx, aiccAcquireHierarchyLua, keys, l.limits.Upstream, l.limits.Org, l.limits.Agent, l.limits.Session, l.ttl.Milliseconds()).Int64()
	if err != nil {
		return nil, fmt.Errorf("aicc 并发额度存储失败: %w", err)
	}
	if res != 1 {
		return nil, ErrAICCConcurrencyLimited
	}
	return func() { _, _ = l.client.Eval(context.Background(), aiccReleaseHierarchyLua, keys).Result() }, nil
}
