package aicc

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/redis"
	"oc-manager/internal/store/sqlc"
)

// TestMessageDispatchLoopTickSweepsReservesAndDispatches 覆盖任务运行循环：
// MySQL 就绪任务先补入 Redis 信号队列，再按批量领取并交给 dispatcher 执行。
func TestMessageDispatchLoopTickSweepsReservesAndDispatches(t *testing.T) {
	queue := redis.NewMemoryQueue()
	store := &messageTaskStoreStub{ready: []sqlc.AiccMessageTask{{ID: "task-1"}, {ID: "task-2"}}}
	dispatcher := &messageTaskDispatcherStub{}
	loop := NewMessageDispatchLoop(store, queue, dispatcher, slog.Default())

	err := loop.Tick(context.Background())

	require.NoError(t, err)
	assert.Equal(t, int32(32), store.limit)
	assert.Equal(t, int64(1), store.recoverCalls)
	assert.Equal(t, []string{"task-1", "task-2"}, dispatcher.dispatchedIDs())
}

// TestMessageDispatchLoopTickRetainsMySQLTaskWhenRedisFails 覆盖 Redis 故障降级：
// 单轮入队失败必须返回错误交给周期 runner 记录，下一轮仍会重新从 MySQL 扫描任务。
func TestMessageDispatchLoopTickRetainsMySQLTaskWhenRedisFails(t *testing.T) {
	store := &messageTaskStoreStub{ready: []sqlc.AiccMessageTask{{ID: "task-1"}}}
	queue := &messageTaskQueueStub{enqueueErr: assert.AnError}
	loop := NewMessageDispatchLoop(store, queue, &messageTaskDispatcherStub{}, slog.Default())

	err := loop.Tick(context.Background())

	require.ErrorIs(t, err, assert.AnError)
	assert.Equal(t, int32(32), store.limit)
	assert.Equal(t, int64(1), store.recoverCalls)
}

// messageTaskStoreStub 记录运行循环对 MySQL 就绪任务扫描和租约回收的调用。
type messageTaskStoreStub struct {
	ready        []sqlc.AiccMessageTask
	limit        int32
	recoverCalls int64
}

func (s *messageTaskStoreStub) ListReadyAICCMessageTasks(_ context.Context, limit int32) ([]sqlc.AiccMessageTask, error) {
	s.limit = limit
	return s.ready, nil
}

func (s *messageTaskStoreStub) RecoverExpiredAICCMessageTaskLeases(context.Context) (int64, error) {
	s.recoverCalls++
	return 0, nil
}

// messageTaskDispatcherStub 捕获实际被循环分派的任务，不依赖运行时或数据库。
type messageTaskDispatcherStub struct{ tasks []sqlc.AiccMessageTask }

func (d *messageTaskDispatcherStub) Dispatch(_ context.Context, task sqlc.AiccMessageTask) error {
	d.tasks = append(d.tasks, task)
	return nil
}

func (d *messageTaskDispatcherStub) dispatchedIDs() []string {
	ids := make([]string, 0, len(d.tasks))
	for _, task := range d.tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

// messageTaskQueueStub 用于模拟 Redis 故障，证明循环不会把 Redis 当作任务事实来源。
type messageTaskQueueStub struct{ enqueueErr error }

func (q *messageTaskQueueStub) Enqueue(context.Context, string) error                 { return q.enqueueErr }
func (*messageTaskQueueStub) EnqueueDelayed(context.Context, string, time.Time) error { return nil }
func (*messageTaskQueueStub) Reserve(context.Context, int) ([]string, error)          { return nil, nil }
