package scheduler

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/store/sqlc"
)

func TestSchedulerTickReenqueuesReadyJobs(t *testing.T) {
	store := &storeStub{ready: makeJobs(t, "00000000-0000-0000-0000-0000000001a1", "00000000-0000-0000-0000-0000000001a2")}
	queue := &queueStub{}

	s := New(store, queue, Config{})
	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if len(queue.enqueued) != 2 {
		t.Fatalf("enqueued = %+v, want 2", queue.enqueued)
	}
}

func TestSchedulerTickPropagatesEnqueueError(t *testing.T) {
	store := &storeStub{ready: makeJobs(t, "00000000-0000-0000-0000-0000000001a1")}
	queue := &queueStub{err: errors.New("redis down")}
	s := New(store, queue, Config{})
	if err := s.Tick(context.Background()); err == nil {
		t.Fatalf("expected error")
	}
}

func TestSchedulerTickAppliesDefaultBatchSize(t *testing.T) {
	store := &storeStub{}
	s := New(store, &queueStub{}, Config{})
	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if store.lastLimit != 100 {
		t.Fatalf("limit = %d, want 100", store.lastLimit)
	}
}

func makeJobs(t *testing.T, ids ...string) []sqlc.Job {
	jobs := make([]sqlc.Job, 0, len(ids))
	for _, id := range ids {
		var uuid pgtype.UUID
		if err := uuid.Scan(id); err != nil {
			t.Fatalf("uuid: %v", err)
		}
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
