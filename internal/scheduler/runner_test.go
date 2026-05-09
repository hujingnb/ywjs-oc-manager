package scheduler

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"oc-manager/internal/store/sqlc"
)

type loopStore struct {
	calls atomic.Int32
	err   error
}

func (s *loopStore) ListReadyJobs(_ context.Context, _ int32) ([]sqlc.Job, error) {
	s.calls.Add(1)
	if s.err != nil {
		return nil, s.err
	}
	return nil, nil
}

type recordingQueue struct{}

func (recordingQueue) Enqueue(_ context.Context, _ string) error { return nil }

func newLoopScheduler(store *loopStore) *Scheduler {
	return New(store, recordingQueue{}, Config{BatchSize: 10})
}

func TestLoop_TickFiresOnInterval(t *testing.T) {
	store := &loopStore{}
	loop := NewLoop(newLoopScheduler(store), 5*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	if err := loop.Run(ctx); err != nil {
		t.Fatalf("Run err = %v", err)
	}
	if store.calls.Load() < 5 {
		t.Fatalf("Tick 调用次数 = %d, want >= 5", store.calls.Load())
	}
}

func TestLoop_LogsTickError(t *testing.T) {
	store := &loopStore{err: errors.New("db down")}
	logBuf := &bytes.Buffer{}
	loop := NewLoop(newLoopScheduler(store), 5*time.Millisecond)
	loop.SetLogger(slog.New(slog.NewTextHandler(logBuf, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := loop.Run(ctx); err != nil {
		t.Fatalf("Run err = %v", err)
	}
	if !strings.Contains(logBuf.String(), "scheduler tick 错误") {
		t.Fatalf("日志缺 tick 错误: %s", logBuf.String())
	}
}

func TestLoop_RejectsMissingScheduler(t *testing.T) {
	loop := NewLoop(nil, 5*time.Millisecond)
	if err := loop.Run(context.Background()); err == nil {
		t.Fatal("缺 scheduler 应当报错")
	}
}

func TestLoop_DefaultInterval(t *testing.T) {
	store := &loopStore{}
	loop := NewLoop(newLoopScheduler(store), 0)
	// 默认 5s 太长，这里只校验"短超时内 ticker 还没触发"逻辑等价于"loop 在等"，
	// 因此用极短 ctx 验证 Run 仍然干净退出。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := loop.Run(ctx); err != nil {
		t.Fatalf("Run err = %v", err)
	}
}
