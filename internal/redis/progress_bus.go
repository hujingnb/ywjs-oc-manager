package redis

import (
	"context"
	"encoding/json"
	"fmt"

	redis "github.com/redis/go-redis/v9"
)

// PhaseDone 是哨兵 phase,follower 收到即视为 leader 完成(成功或失败由 Err 字段决定)。
// 不与任何真实 status 值冲突。
const PhaseDone = "__done__"

// ProgressEvent 是镜像拉取期间 leader 广播给 follower 的进度。
// Phase 取值:"pulling_runtime_image" / PhaseDone(完成哨兵)。
// Total=0 表示未知,前端展示为不定进度。
type ProgressEvent struct {
	Phase   string `json:"phase"`
	Current int64  `json:"current"`
	Total   int64  `json:"total"`
	// ErrMessage 仅在 Phase=PhaseDone 时使用;空表示成功。
	ErrMessage string `json:"err,omitempty"`
}

// BusMessage 是 Subscribe 通道里的一条消息。
// Err 是把 ProgressEvent.ErrMessage 反序列化后的 Go error,便于调用方直接 if err。
type BusMessage struct {
	Channel string
	Event   ProgressEvent
	Err     error
}

// ProgressBus 抽象跨 manager 进度广播,实现走 Redis Pub/Sub。
type ProgressBus interface {
	Publish(ctx context.Context, channel string, event ProgressEvent) error
	// PublishDone 发出 phase=PhaseDone 的事件;err=nil 表示成功。
	PublishDone(ctx context.Context, channel string, err error) error
	// Subscribe 订阅一个或多个 channel;返回的 cancel 用于释放底层 PubSub 连接。
	Subscribe(ctx context.Context, channels ...string) (<-chan BusMessage, func(), error)
}

// RedisProgressBus 基于 redis Pub/Sub。
type RedisProgressBus struct {
	client *redis.Client
}

// NewRedisProgressBus 创建实例;client 必须有 Subscribe 能力(*redis.Client / Cluster)。
func NewRedisProgressBus(client *redis.Client) *RedisProgressBus {
	return &RedisProgressBus{client: client}
}

// Publish 见接口注释。
func (b *RedisProgressBus) Publish(ctx context.Context, channel string, event ProgressEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("序列化 ProgressEvent: %w", err)
	}
	return b.client.Publish(ctx, channel, body).Err()
}

// PublishDone 见接口注释。
func (b *RedisProgressBus) PublishDone(ctx context.Context, channel string, err error) error {
	event := ProgressEvent{Phase: PhaseDone}
	if err != nil {
		event.ErrMessage = err.Error()
	}
	return b.Publish(ctx, channel, event)
}

// Subscribe 见接口注释。
// 返回的 cancel 必须被调用,否则 redis 连接会泄漏。
func (b *RedisProgressBus) Subscribe(ctx context.Context, channels ...string) (<-chan BusMessage, func(), error) {
	pubsub := b.client.Subscribe(ctx, channels...)
	// 等待 subscribe 命令真正发出去,避免 Publish 比 Subscribe 早导致首条消息丢失。
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		return nil, nil, fmt.Errorf("订阅 redis channel 失败: %w", err)
	}
	out := make(chan BusMessage, 16)
	go func() {
		defer close(out)
		raw := pubsub.Channel()
		for msg := range raw {
			var event ProgressEvent
			if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
				out <- BusMessage{Channel: msg.Channel, Err: fmt.Errorf("解析 ProgressEvent: %w", err)}
				continue
			}
			busMsg := BusMessage{Channel: msg.Channel, Event: event}
			if event.ErrMessage != "" {
				busMsg.Err = fmt.Errorf("%s", event.ErrMessage)
			}
			out <- busMsg
		}
	}()
	cancel := func() { _ = pubsub.Close() }
	return out, cancel, nil
}
