package redis

import (
	"context"
	"errors"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestBus 连接本地 manager-redis,缺失时 Skip。
//
// 与 newTestLocker 共用 DB 11(同包内串行,不会互相 FlushDB 清干净),
// 与 imagecoord 包测试(DB 12)隔离,详见 dist_locker_test.go 说明。
func newTestBus(t *testing.T) (*RedisProgressBus, *redis.Client, func()) {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379", Password: "123456", DB: 11})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skip("本地 Redis 不可用,跳过 ProgressBus 集成测试: " + err.Error())
	}
	bus := NewRedisProgressBus(client)
	cleanup := func() {
		_ = client.FlushDB(context.Background()).Err()
		_ = client.Close()
	}
	return bus, client, cleanup
}

// TestProgressBus_PubSub 验证 publish 后 subscribe 能收到事件。
// 注意:Redis Pub/Sub 没有持久化,必须先 Subscribe 再 Publish。
func TestProgressBus_PubSub(t *testing.T) {
	bus, _, cleanup := newTestBus(t)
	defer cleanup()
	ctx := context.Background()
	ch, cancel, err := bus.Subscribe(ctx, "ocm:test:bus:foo")
	require.NoError(t, err)
	defer cancel()

	// 给 subscriber 一点时间完成 SUBSCRIBE 握手再 publish
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, bus.Publish(ctx, "ocm:test:bus:foo", ProgressEvent{Phase: "pulling_image", Current: 50, Total: 100}))

	select {
	case msg := <-ch:
		assert.Equal(t, "ocm:test:bus:foo", msg.Channel)
		assert.Equal(t, "pulling_image", msg.Event.Phase)
		assert.EqualValues(t, 50, msg.Event.Current)
		assert.EqualValues(t, 100, msg.Event.Total)
		assert.NoError(t, msg.Err)
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber 没有收到事件")
	}
}

// TestProgressBus_DoneSentinel __done__ 哨兵能携带 err 字符串,被 follower 识别为完成。
func TestProgressBus_DoneSentinel(t *testing.T) {
	bus, _, cleanup := newTestBus(t)
	defer cleanup()
	ctx := context.Background()
	ch, cancel, err := bus.Subscribe(ctx, "ocm:test:bus:done")
	require.NoError(t, err)
	defer cancel()
	time.Sleep(100 * time.Millisecond)

	require.NoError(t, bus.PublishDone(ctx, "ocm:test:bus:done", errors.New("pull aborted")))

	select {
	case msg := <-ch:
		assert.Equal(t, PhaseDone, msg.Event.Phase)
		require.Error(t, msg.Err)
		assert.Contains(t, msg.Err.Error(), "pull aborted")
	case <-time.After(2 * time.Second):
		t.Fatal("没收到 done 事件")
	}
}
