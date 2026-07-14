package aicc

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/redis"
	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// TestMessageDispatchLoopTelemetryCoversQueueConcurrencyAndRecovery 覆盖真实 Tick 路径：
// 队列等待、并发已满和重启租约回收必须经同一 observer 输出，且不能携带访客内容。
func TestMessageDispatchLoopTelemetryCoversQueueConcurrencyAndRecovery(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	queue := redis.NewMemoryQueue()
	store := &messageTaskStoreStub{ready: []sqlc.AiccMessageTask{
		{ID: "task-1", AgentID: "agent-1", OrgID: "org-1", CreatedAt: time.Now().Add(-time.Second)}, // 首个在飞任务。
		{ID: "task-2", AgentID: "agent-1", OrgID: "org-1", CreatedAt: time.Now().Add(-time.Second)}, // 第二个在飞任务。
		{ID: "task-3", AgentID: "agent-1", OrgID: "org-1", CreatedAt: time.Now().Add(-time.Second)}, // 第三个在飞任务。
		{ID: "task-4", AgentID: "agent-1", OrgID: "org-1", CreatedAt: time.Now().Add(-time.Second)}, // 第四个在飞任务。
		{ID: "task-5", AgentID: "agent-1", OrgID: "org-1", CreatedAt: time.Now().Add(-time.Second)}, // 并发满后等待下轮。
	}}
	dispatcher := &concurrentMessageTaskDispatcher{release: make(chan struct{})}
	loop := NewMessageDispatchLoop(store, queue, dispatcher, logger)
	loop.SetObserver(service.NewSlogAICCDispatchObserver(logger))

	require.NoError(t, loop.Tick(context.Background()))
	require.Eventually(t, func() bool { return dispatcher.activeCount() == aiccMessageDispatchConcurrency }, time.Second, 10*time.Millisecond)

	logs := output.String()
	assert.Contains(t, logs, `"aicc_event":"queued"`)
	assert.Contains(t, logs, `"aicc_event":"lease_recovered"`)
	assert.Contains(t, logs, `"queue_wait_ms":`)
	assert.Contains(t, logs, `"inflight":4`)
	assert.NotContains(t, logs, "visitor-content")
	assert.NotContains(t, logs, "token")
	close(dispatcher.release)
	loop.Wait()
}

// TestMessageDispatchLoopTelemetryClassifiesDispatchError 覆盖分派失败日志：
// 运行时错误即使包含访客内容或令牌字样，也只能输出固定错误分类，不得直接写出错误文本。
func TestMessageDispatchLoopTelemetryClassifiesDispatchError(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	queue := redis.NewMemoryQueue()
	store := &messageTaskStoreStub{ready: []sqlc.AiccMessageTask{{ID: "task-1", AgentID: "agent-1", OrgID: "org-1", CreatedAt: time.Now()}}}
	loop := NewMessageDispatchLoop(store, queue, errorMessageTaskDispatcher{err: errors.New("visitor-content token=secret")}, logger)
	loop.SetObserver(service.NewSlogAICCDispatchObserver(logger))

	require.NoError(t, loop.Tick(context.Background()))
	loop.Wait()

	logs := output.String()
	assert.Contains(t, logs, `"result":"dispatch_runtime_error"`)
	assert.NotContains(t, logs, "visitor-content")
	assert.NotContains(t, logs, "secret")
	assert.False(t, strings.Contains(logs, `"error"`))
}

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
	assert.Equal(t, int64(1), dispatcher.recoveryCount())
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
	assert.Equal(t, int64(2), dispatcher.recoveryCount())

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
	ready []sqlc.AiccMessageTask
	limit int32
}

func (s *messageTaskStoreStub) ListReadyAICCMessageTasks(_ context.Context, limit int32) ([]sqlc.AiccMessageTask, error) {
	s.limit = limit
	return s.ready, nil
}

// errorMessageTaskDispatcher 模拟包含敏感上下文的运行时错误，验证循环不会原样记录。
type errorMessageTaskDispatcher struct{ err error }

func (d errorMessageTaskDispatcher) Dispatch(context.Context, sqlc.AiccMessageTask) error {
	return d.err
}
func (errorMessageTaskDispatcher) RecoverExpiredLeases(context.Context) (int64, error) { return 0, nil }

// messageTaskDispatcherStub 捕获实际被循环分派的任务，不依赖运行时或数据库。
type messageTaskDispatcherStub struct {
	mu           sync.Mutex
	tasks        []sqlc.AiccMessageTask
	recoverCalls int64
}

func (d *messageTaskDispatcherStub) Dispatch(_ context.Context, task sqlc.AiccMessageTask) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.tasks = append(d.tasks, task)
	return nil
}

func (d *messageTaskDispatcherStub) RecoverExpiredLeases(context.Context) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.recoverCalls++
	return 0, nil
}

func (d *messageTaskDispatcherStub) recoveryCount() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.recoverCalls
}

// blockingMessageTaskDispatcher 模拟被上游运行时长时间占用的单条消息分派。
type blockingMessageTaskDispatcher struct {
	started      chan struct{}
	release      chan struct{}
	recoverCalls int64
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

func (*concurrentMessageTaskDispatcher) RecoverExpiredLeases(context.Context) (int64, error) {
	return 1, nil
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

func (d *blockingMessageTaskDispatcher) RecoverExpiredLeases(context.Context) (int64, error) {
	d.recoverCalls++
	return 0, nil
}

func (d *blockingMessageTaskDispatcher) recoveryCount() int64 { return d.recoverCalls }

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
