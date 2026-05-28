package worker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"oc-manager/internal/store/sqlc"
	"oc-manager/internal/worker/handlers"
)

// countingStore 在 Tick 调用 Reserve 后立刻返回空，确保 Tick 快速跑完。
// 同时统计调用次数，方便断言并发执行情况。
// JobStore 迁移为 MySQL/string 接口：所有方法仅返回 error（:exec 语义）。
type countingStore struct{}

func (countingStore) GetJob(_ context.Context, _ string) (sqlc.Job, error) {
	return sqlc.Job{}, errors.New("unused")
}
func (countingStore) MarkJobRunning(_ context.Context, _ sqlc.MarkJobRunningParams) error {
	return nil
}
func (countingStore) MarkJobSucceeded(_ context.Context, _ string) error {
	return nil
}
func (countingStore) MarkJobFailed(_ context.Context, _ sqlc.MarkJobFailedParams) error {
	return nil
}
func (countingStore) RetryJob(_ context.Context, _ sqlc.RetryJobParams) error {
	return nil
}

type countingQueue struct {
	calls atomic.Int32
}

func (q *countingQueue) Reserve(_ context.Context, _ int) ([]string, error) {
	q.calls.Add(1)
	return nil, nil
}

// panicQueue 让 Reserve 在前几次调用 panic，验证 panic 拦截。
type panicQueue struct {
	panics    atomic.Int32
	remaining atomic.Int32
}

func (q *panicQueue) Reserve(_ context.Context, _ int) ([]string, error) {
	if q.remaining.Add(-1) >= 0 {
		q.panics.Add(1)
		panic("boom")
	}
	return nil, nil
}

func newWorker(queue Queue) *Worker {
	return New(countingStore{}, queue, handlers.NewRegistry(), Config{BatchSize: 1, WorkerID: "test"})
}

// TestPool_RunsConcurrently 验证池RunsConcurrently的特殊分支或幂等场景。
func TestPool_RunsConcurrently(t *testing.T) {
	q := &countingQueue{}
	pool := NewPool(newWorker(q), 4, 5*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	err := pool.Run(ctx)
	require.NoError(t, err)
	calls := q.calls.Load()
	if calls < 8 {
		// 4 goroutine × ~16 tick 应至少触发几十次；过低意味着 ticker 没并发跑。
		t.Fatalf("Reserve 调用次数 = %d, want >= 8", calls)
	}
}

// TestPool_PanicIsolatedAndLogged 验证池panicIsolated并Logged的预期行为场景。
func TestPool_PanicIsolatedAndLogged(t *testing.T) {
	q := &panicQueue{}
	q.remaining.Store(3)
	logBuf := &bytes.Buffer{}
	pool := NewPool(newWorker(q), 2, 5*time.Millisecond)
	pool.SetLogger(slog.New(slog.NewTextHandler(logBuf, nil)))

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	err := pool.Run(ctx)
	require.NoError(t, err)
	if q.panics.Load() < 3 {
		t.Fatalf("panic 次数 = %d, want >= 3", q.panics.Load())
	}
	require.True(t, strings.Contains(logBuf.String(), "panic"))
}

// TestPool_RejectsMissingWorker 验证池拒绝缺失worker的异常或拒绝路径场景。
func TestPool_RejectsMissingWorker(t *testing.T) {
	pool := NewPool(nil, 2, 5*time.Millisecond)
	err := pool.Run(context.Background())
	require.Error(t, err)
}

// TestPool_DefaultConcurrencyAndInterval 验证池默认值Concurrency并Interval的边界条件场景。
func TestPool_DefaultConcurrencyAndInterval(t *testing.T) {
	q := &countingQueue{}
	pool := NewPool(newWorker(q), 0, 0)
	// 默认 interval 是 200ms，留 350ms 余量保证至少触发一次。
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	err := pool.Run(ctx)
	require.NoError(t, err)
	require.NotEqual(t, 0, q.calls.Load())
}
