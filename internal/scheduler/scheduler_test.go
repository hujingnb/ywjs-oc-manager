// Package scheduler 的 scheduler_test 覆盖调度器按时间窗口重入队 pending job 的行为。
package scheduler

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"oc-manager/internal/store/sqlc"
)

// TestSchedulerTickReenqueuesReadyJobs 验证调度器TickReenqueuesReady任务的预期行为场景。
func TestSchedulerTickReenqueuesReadyJobs(t *testing.T) {
	store := &storeStub{ready: makeJobs("00000000-0000-0000-0000-0000000001a1", "00000000-0000-0000-0000-0000000001a2")}
	queue := &queueStub{}

	s := New(store, queue, Config{})
	err := s.Tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, len(queue.enqueued))
}

// TestSchedulerTickPropagatesEnqueueError 验证调度器Tick透传Enqueue错误的错误映射或错误记录场景。
func TestSchedulerTickPropagatesEnqueueError(t *testing.T) {
	store := &storeStub{ready: makeJobs("00000000-0000-0000-0000-0000000001a1")}
	queue := &queueStub{err: errors.New("redis down")}
	s := New(store, queue, Config{})
	err := s.Tick(context.Background())
	require.Error(t, err)
}

// TestSchedulerTickAppliesDefaultBatchSize 验证调度器Tick应用默认值BatchSize的边界条件场景。
func TestSchedulerTickAppliesDefaultBatchSize(t *testing.T) {
	store := &storeStub{}
	s := New(store, &queueStub{}, Config{})
	err := s.Tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, int32(100), store.lastLimit)
}

// makeJobs 根据 id 字符串列表构造 sqlc.Job 切片; ID 现为 string。
func makeJobs(ids ...string) []sqlc.Job {
	jobs := make([]sqlc.Job, 0, len(ids))
	for _, id := range ids {
		jobs = append(jobs, sqlc.Job{ID: id})
	}
	return jobs
}

type storeStub struct {
	ready     []sqlc.Job
	lastLimit int32
}

func (s *storeStub) ListReadyJobs(_ context.Context, limit int32) ([]sqlc.Job, error) {
	s.lastLimit = limit
	return s.ready, nil
}

type queueStub struct {
	enqueued []string
	err      error
}

func (q *queueStub) Enqueue(_ context.Context, id string) error {
	if q.err != nil {
		return q.err
	}
	q.enqueued = append(q.enqueued, id)
	return nil
}
