// Package redis 维护 manager 与 Redis 之间的通信能力。
// 调度器使用 Redis 作为快速信号通道，PostgreSQL 仍是 job 事实来源；
// 任何因 Redis 重启或数据丢失导致的信号缺失，scheduler 都会通过扫库的方式重新入队。
package redis

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// Queue 抽象任务信号通道。
// 所有方法保持幂等：重复 Enqueue 同一个 jobID 不会破坏调度顺序，重复 Reserve 也只会拿到一次。
type Queue interface {
	// Enqueue 立即可执行的任务推入队列。
	Enqueue(ctx context.Context, jobID string) error
	// EnqueueDelayed 延迟到 runAfter 时刻再可见的任务推入队列。
	EnqueueDelayed(ctx context.Context, jobID string, runAfter time.Time) error
	// Reserve 取出 limit 条目前到期的任务 ID。
	Reserve(ctx context.Context, limit int) ([]string, error)
}

// ErrQueueClosed 表示底层 Redis 客户端已关闭。
var ErrQueueClosed = errors.New("redis queue 已关闭")

// RedisQueue 是基于 Redis ZSET 的队列实现。
// 使用单一 ZSET：member 为 jobID，score 为可执行时间（毫秒）。
// Reserve 通过 ZRANGEBYSCORE+ZREM 原子取出并删除，避免重复消费。
type RedisQueue struct {
	client *redis.Client
	key    string
	now    func() time.Time
}

// Config 描述 Redis 连接参数。
type Config struct {
	// Addr 是 Redis 地址，格式通常为 host:port。
	Addr string
	// Password 是 Redis AUTH 密码；空值表示不鉴权。
	Password string
	// DB 是 Redis 逻辑库编号。
	DB int
	// QueueKey 是 ZSET key；为空时使用 ocm:jobs:queue。
	QueueKey string
}

// NewRedisQueue 创建 Redis 队列实现。
func NewRedisQueue(cfg Config) *RedisQueue {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	key := cfg.QueueKey
	if key == "" {
		key = "ocm:jobs:queue"
	}
	return &RedisQueue{client: client, key: key, now: time.Now}
}

// Close 关闭底层连接。
func (q *RedisQueue) Close() error {
	if q.client == nil {
		return nil
	}
	return q.client.Close()
}

// Enqueue 等价于 EnqueueDelayed(ctx, jobID, now)。
func (q *RedisQueue) Enqueue(ctx context.Context, jobID string) error {
	if q.client == nil {
		return ErrQueueClosed
	}
	return q.EnqueueDelayed(ctx, jobID, q.currentTime())
}

func (q *RedisQueue) currentTime() time.Time {
	if q.now != nil {
		return q.now()
	}
	return time.Now()
}

// EnqueueDelayed 将 jobID 写入 ZSET，score 为目标时间毫秒数。
func (q *RedisQueue) EnqueueDelayed(ctx context.Context, jobID string, runAfter time.Time) error {
	if q.client == nil {
		return ErrQueueClosed
	}
	score := float64(runAfter.UnixMilli())
	return q.client.ZAdd(ctx, q.key, redis.Z{Score: score, Member: jobID}).Err()
}

// Reserve 弹出当前可执行的 jobID。
// 实现使用 ZRANGEBYSCORE+ZREM 的 pipeline：先列出，再逐个 ZREM；ZREM 返回 1 才视为本节点抢到。
// 这样多副本 worker 同时调用时只会有一个拿到具体 jobID。
func (q *RedisQueue) Reserve(ctx context.Context, limit int) ([]string, error) {
	if q.client == nil {
		return nil, ErrQueueClosed
	}
	if limit <= 0 {
		return nil, nil
	}
	ids, err := q.client.ZRangeByScore(ctx, q.key, &redis.ZRangeBy{
		Min:    "-inf",
		Max:    formatFloat(float64(q.currentTime().UnixMilli())),
		Offset: 0,
		Count:  int64(limit),
	}).Result()
	if err != nil {
		return nil, err
	}
	reserved := make([]string, 0, len(ids))
	for _, id := range ids {
		removed, err := q.client.ZRem(ctx, q.key, id).Result()
		if err != nil {
			return nil, err
		}
		if removed == 1 {
			reserved = append(reserved, id)
		}
	}
	return reserved, nil
}

// formatFloat 用 strconv 避免在 hot path 引入 fmt 反射。
func formatFloat(value float64) string {
	const prec = -1
	return strconvFormatFloat(value, prec)
}

// strconvFormatFloat 拆分出来便于在测试中替换。
var strconvFormatFloat = func(value float64, prec int) string {
	return formatFloatRaw(value, prec)
}

// MemoryQueue 是内存版队列，仅用于单元测试。
// 它复用与 RedisQueue 相同的 score 模型，便于在 worker 测试中替换底层。
type MemoryQueue struct {
	mu      sync.Mutex
	entries []memoryEntry
	now     func() time.Time
}

type memoryEntry struct {
	// jobID 与 Redis ZSET member 一致，必须是 jobs.id 的字符串形式。
	jobID string
	// score 与 RedisQueue 一致，使用 run_after 的 Unix 毫秒时间戳。
	score int64
}

// NewMemoryQueue 创建内存队列。
func NewMemoryQueue() *MemoryQueue { return &MemoryQueue{now: time.Now} }

// SetClock 替换内存队列的时钟，便于测试中模拟延迟到期。
func (q *MemoryQueue) SetClock(now func() time.Time) { q.now = now }

// Enqueue 内存版的立即入队。
func (q *MemoryQueue) Enqueue(_ context.Context, jobID string) error {
	return q.add(jobID, q.now())
}

// EnqueueDelayed 内存版的延迟入队。
func (q *MemoryQueue) EnqueueDelayed(_ context.Context, jobID string, runAfter time.Time) error {
	return q.add(jobID, runAfter)
}

func (q *MemoryQueue) add(jobID string, runAfter time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, entry := range q.entries {
		if entry.jobID == jobID {
			// 与 Redis ZADD 默认语义保持一致：同一 jobID 重复入队不会产生多个成员。
			return nil
		}
	}
	q.entries = append(q.entries, memoryEntry{jobID: jobID, score: runAfter.UnixMilli()})
	sort.SliceStable(q.entries, func(i, j int) bool { return q.entries[i].score < q.entries[j].score })
	return nil
}

// Reserve 弹出当前可执行的任务。
func (q *MemoryQueue) Reserve(_ context.Context, limit int) ([]string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if limit <= 0 {
		return nil, nil
	}
	now := q.now().UnixMilli()
	reserved := make([]string, 0, limit)
	remaining := q.entries[:0]
	for _, entry := range q.entries {
		if len(reserved) < limit && entry.score <= now {
			reserved = append(reserved, entry.jobID)
			continue
		}
		remaining = append(remaining, entry)
	}
	q.entries = remaining
	return reserved, nil
}

// Pending 返回当前积压（未到期）的 jobID 顺序，仅供测试断言。
func (q *MemoryQueue) Pending() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]string, 0, len(q.entries))
	for _, entry := range q.entries {
		out = append(out, entry.jobID)
	}
	return out
}
