package worker

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/store/sqlc"
	"oc-manager/internal/worker/handlers"
)

// countingStore 在 Tick 调用 Reserve 后立刻返回空，确保 Tick 快速跑完。
// 同时统计调用次数，方便断言并发执行情况。
type countingStore struct{}

func (countingStore) GetJob(_ context.Context, _ pgtype.UUID) (sqlc.Job, error) {
	return sqlc.Job{}, errors.New("unused")
}
func (countingStore) MarkJobRunning(_ context.Context, _ sqlc.MarkJobRunningParams) (sqlc.Job, error) {
	return sqlc.Job{}, nil
}
func (countingStore) MarkJobSucceeded(_ context.Context, _ pgtype.UUID) (sqlc.Job, error) {
	return sqlc.Job{}, nil
}
func (countingStore) MarkJobFailed(_ context.Context, _ sqlc.MarkJobFailedParams) (sqlc.Job, error) {
	return sqlc.Job{}, nil
}
func (countingStore) RetryJob(_ context.Context, _ sqlc.RetryJobParams) (sqlc.Job, error) {
	return sqlc.Job{}, nil
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

func TestPool_RunsConcurrently(t *testing.T) {
	q := &countingQueue{}
	pool := NewPool(newWorker(q), 4, 5*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	if err := pool.Run(ctx); err != nil {
		t.Fatalf("Run err = %v", err)
	}
	calls := q.calls.Load()
	if calls < 8 {
		// 4 goroutine × ~16 tick 应至少触发几十次；过低意味着 ticker 没并发跑。
		t.Fatalf("Reserve 调用次数 = %d, want >= 8", calls)
	}
}

func TestPool_PanicIsolatedAndLogged(t *testing.T) {
	q := &panicQueue{}
	q.remaining.Store(3)
	logBuf := &bytes.Buffer{}
	pool := NewPool(newWorker(q), 2, 5*time.Millisecond)
	pool.SetLogger(log.New(logBuf, "", 0))

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if err := pool.Run(ctx); err != nil {
		t.Fatalf("Run err = %v", err)
	}
	if q.panics.Load() < 3 {
		t.Fatalf("panic 次数 = %d, want >= 3", q.panics.Load())
	}
	if !strings.Contains(logBuf.String(), "panic") {
		t.Fatalf("日志未包含 panic: %s", logBuf.String())
	}
}

func TestPool_RejectsMissingWorker(t *testing.T) {
	pool := NewPool(nil, 2, 5*time.Millisecond)
	if err := pool.Run(context.Background()); err == nil {
		t.Fatal("缺 worker 时应返回错误")
	}
}

func TestPool_DefaultConcurrencyAndInterval(t *testing.T) {
	q := &countingQueue{}
	pool := NewPool(newWorker(q), 0, 0)
	// 默认 interval 是 200ms，留 350ms 余量保证至少触发一次。
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	if err := pool.Run(ctx); err != nil {
		t.Fatalf("Run err = %v", err)
	}
	if q.calls.Load() == 0 {
		t.Fatal("默认 interval 下 ticker 至少触发一次")
	}
}
