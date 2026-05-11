package scheduler

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stretchr/testify/require"
	"oc-manager/internal/store/sqlc"
)

func TestSchedulerTickReenqueuesReadyJobs(t *testing.T) {
	store := &storeStub{ready: makeJobs(t, "00000000-0000-0000-0000-0000000001a1", "00000000-0000-0000-0000-0000000001a2")}
	queue := &queueStub{}

	s := New(store, queue, Config{})
	err := s.Tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, len(queue.enqueued))
}

func TestSchedulerTickPropagatesEnqueueError(t *testing.T) {
	store := &storeStub{ready: makeJobs(t, "00000000-0000-0000-0000-0000000001a1")}
	queue := &queueStub{err: errors.New("redis down")}
	s := New(store, queue, Config{})
	err := s.Tick(context.Background())
	require.Error(t, err)
}

func TestSchedulerTickAppliesDefaultBatchSize(t *testing.T) {
	store := &storeStub{}
	s := New(store, &queueStub{}, Config{})
	err := s.Tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, int32(100), store.lastLimit)
}

func makeJobs(t *testing.T, ids ...string) []sqlc.Job {
	jobs := make([]sqlc.Job, 0, len(ids))
	for _, id := range ids {
		var uuid pgtype.UUID
		err := uuid.Scan(id)
		require.NoError(t, err)
		jobs = append(jobs, sqlc.Job{ID: uuid})
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
