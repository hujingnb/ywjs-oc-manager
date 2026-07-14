package aicc

import (
	"context"
	"log/slog"
	"sync"
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
	require.Eventually(t, func() bool { return len(dispatcher.dispatchedIDs()) == 2 }, time.Second, 10*time.Millisecond)
	assert.ElementsMatch(t, []string{"task-1", "task-2"}, dispatcher.dispatchedIDs())
	loop.Wait()
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

// TestMessageDispatchLoopTickDoesNotBlockScanOnSlowDispatch 覆盖慢速运行时调用：
// dispatcher 正在处理一个任务时，下一轮 Tick 仍必须完成租约回收和 MySQL 扫描。
func TestMessageDispatchLoopTickDoesNotBlockScanOnSlowDispatch(t *testing.T) {
	queue := redis.NewMemoryQueue()
	store := &messageTaskStoreStub{ready: []sqlc.AiccMessageTask{{ID: "task-1"}}}
	dispatcher := &blockingMessageTaskDispatcher{started: make(chan struct{}), release: make(chan struct{})}
	loop := NewMessageDispatchLoop(store, queue, dispatcher, slog.Default())

	require.NoError(t, loop.Tick(context.Background()))
	require.Eventually(t, func() bool {
		select {
		case <-dispatcher.started:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	start := time.Now()
	require.NoError(t, loop.Tick(context.Background()))
	assert.Less(t, time.Since(start), 100*time.Millisecond)
	assert.Equal(t, int64(2), store.recoverCalls)

	close(dispatcher.release)
	loop.Wait()
}

// TestMessageDispatchLoopBoundsSlowRuntimeConcurrency 覆盖运行时保护：
// 一个批次超过额度时只允许固定数量的慢调用并发执行，剩余任务留待后续 MySQL 扫描恢复。
func TestMessageDispatchLoopBoundsSlowRuntimeConcurrency(t *testing.T) {
	queue := redis.NewMemoryQueue()
	store := &messageTaskStoreStub{ready: []sqlc.AiccMessageTask{{ID: "task-1"}, {ID: "task-2"}, {ID: "task-3"}, {ID: "task-4"}, {ID: "task-5"}}}
	dispatcher := &concurrentMessageTaskDispatcher{release: make(chan struct{})}
	loop := NewMessageDispatchLoop(store, queue, dispatcher, slog.Default())

	require.NoError(t, loop.Tick(context.Background()))
	require.Eventually(t, func() bool { return dispatcher.activeCount() == aiccMessageDispatchConcurrency }, time.Second, 10*time.Millisecond)
	assert.Equal(t, aiccMessageDispatchConcurrency, dispatcher.maxActiveCount())

	close(dispatcher.release)
	loop.Wait()
}

// TestMessageDispatchLoopRunWaitsForSubmittedDispatchOnShutdown 覆盖退出生命周期：
// 收到取消信号后，Run 必须等待已启动的 runtime 调用退出，不能留下后台 goroutine。
func TestMessageDispatchLoopRunWaitsForSubmittedDispatchOnShutdown(t *testing.T) {
	queue := redis.NewMemoryQueue()
	store := &messageTaskStoreStub{ready: []sqlc.AiccMessageTask{{ID: "task-1"}}}
	dispatcher := &blockingMessageTaskDispatcher{started: make(chan struct{}), release: make(chan struct{})}
	loop := NewMessageDispatchLoop(store, queue, dispatcher, slog.Default())
	loop.interval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()
	require.Eventually(t, func() bool {
		select {
		case <-dispatcher.started:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	cancel()
	select {
	case err := <-done:
		require.Failf(t, "Run 过早返回", "运行时调用结束前返回: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(dispatcher.release)
	require.NoError(t, <-done)
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
type messageTaskDispatcherStub struct {
	mu    sync.Mutex
	tasks []sqlc.AiccMessageTask
}

func (d *messageTaskDispatcherStub) Dispatch(_ context.Context, task sqlc.AiccMessageTask) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.tasks = append(d.tasks, task)
	return nil
}

// blockingMessageTaskDispatcher 模拟被上游运行时长时间占用的单条消息分派。
type blockingMessageTaskDispatcher struct {
	started chan struct{}
	release chan struct{}
}

// concurrentMessageTaskDispatcher 记录同时阻塞的调用数量，用于验证循环并发上限。
type concurrentMessageTaskDispatcher struct {
	mu        sync.Mutex
	active    int
	maxActive int
	release   chan struct{}
}

func (d *concurrentMessageTaskDispatcher) Dispatch(context.Context, sqlc.AiccMessageTask) error {
	d.mu.Lock()
	d.active++
	if d.active > d.maxActive {
		d.maxActive = d.active
	}
	d.mu.Unlock()
	<-d.release
	d.mu.Lock()
	d.active--
	d.mu.Unlock()
	return nil
}

func (d *concurrentMessageTaskDispatcher) activeCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.active
}

func (d *concurrentMessageTaskDispatcher) maxActiveCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.maxActive
}

func (d *blockingMessageTaskDispatcher) Dispatch(context.Context, sqlc.AiccMessageTask) error {
	select {
	case <-d.started:
	default:
		close(d.started)
	}
	<-d.release
	return nil
}

func (d *messageTaskDispatcherStub) dispatchedIDs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
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
