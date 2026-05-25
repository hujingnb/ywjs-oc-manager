// Package worker 的 worker_test 覆盖 worker 对 job 状态推进、重试和队列确认的处理。
package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stretchr/testify/require"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
	"oc-manager/internal/worker/handlers"
)

// TestWorkerTickMarksSuccess 验证workerTickMarks成功的成功路径场景。
func TestWorkerTickMarksSuccess(t *testing.T) {
	store := newJobStoreStub(t)
	registry := handlers.NewRegistry()
	calls := 0
	registry.MustRegister("noop", func(ctx context.Context, job sqlc.Job) error {
		calls++
		return nil
	})
	queue := &queueStub{ids: []string{store.id("job-1")}}
	store.put("job-1", sqlc.Job{ID: store.uuidOf("job-1"), Type: "noop", Status: domain.JobStatusPending, MaxAttempts: 3})

	w := New(store, queue, registry, Config{WorkerID: "w1"})
	err := w.Tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, domain.JobStatusSucceeded, store.snapshot("job-1").Status)
}

// TestWorkerTickRetriesUntilMaxAttempts 验证workerTickRetriesUntil最大Attempts的边界条件场景。
func TestWorkerTickRetriesUntilMaxAttempts(t *testing.T) {
	store := newJobStoreStub(t)
	registry := handlers.NewRegistry()
	registry.MustRegister("flaky", func(_ context.Context, _ sqlc.Job) error { return errors.New("boom") })
	queue := &queueStub{ids: []string{store.id("job-1")}}
	store.put("job-1", sqlc.Job{ID: store.uuidOf("job-1"), Type: "flaky", Status: domain.JobStatusPending, MaxAttempts: 2})

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w := New(store, queue, registry, Config{WorkerID: "w1", BackoffBase: time.Second, BackoffFactor: 2, BackoffMax: 10 * time.Second})
	w.SetClock(func() time.Time { return now })

	err := w.Tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, domain.JobStatusPending, store.snapshot("job-1").Status)
	require.Equal(t, time.Second, store.snapshot("job-1").RunAfter.Time.Sub(now))

	queue.ids = []string{store.id("job-1")}
	store.snapshot("job-1") // ensure visible
	pending := store.snapshot("job-1")
	pending.Status = domain.JobStatusPending
	store.put("job-1", pending)

	err = w.Tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, domain.JobStatusFailed, store.snapshot("job-1").Status)
}

// TestWorkerTickSkipsAlreadyClaimedJobs 验证workerTick跳过已经Claimed任务的特殊分支或幂等场景。
func TestWorkerTickSkipsAlreadyClaimedJobs(t *testing.T) {
	store := newJobStoreStub(t)
	registry := handlers.NewRegistry()
	registry.MustRegister("noop", func(_ context.Context, _ sqlc.Job) error { return nil })
	queue := &queueStub{ids: []string{store.id("job-1")}}
	store.put("job-1", sqlc.Job{ID: store.uuidOf("job-1"), Type: "noop", Status: domain.JobStatusRunning, MaxAttempts: 1})

	w := New(store, queue, registry, Config{WorkerID: "w1"})
	err := w.Tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, store.markRunningCalls)
}

// TestWorkerTickMarksFailedForUnknownType 验证workerTickMarks失败针对未知类型的预期行为场景。
func TestWorkerTickMarksFailedForUnknownType(t *testing.T) {
	store := newJobStoreStub(t)
	registry := handlers.NewRegistry()
	queue := &queueStub{ids: []string{store.id("job-1")}}
	store.put("job-1", sqlc.Job{ID: store.uuidOf("job-1"), Type: "missing", Status: domain.JobStatusPending, MaxAttempts: 3})

	w := New(store, queue, registry, Config{WorkerID: "w1"})
	err := w.Tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, domain.JobStatusFailed, store.snapshot("job-1").Status)
}

type queueStub struct {
	ids []string
}

func (q *queueStub) Reserve(_ context.Context, _ int) ([]string, error) {
	out := q.ids
	q.ids = nil
	return out, nil
}

type jobStoreStub struct {
	t                *testing.T
	jobs             map[string]sqlc.Job
	uuidByName       map[string]pgtype.UUID
	markRunningCalls int
}

func newJobStoreStub(t *testing.T) *jobStoreStub {
	return &jobStoreStub{
		t:          t,
		jobs:       map[string]sqlc.Job{},
		uuidByName: map[string]pgtype.UUID{},
	}
}

func (s *jobStoreStub) id(name string) string {
	uuid := s.uuidOf(name)
	return formatUUID(uuid)
}

func (s *jobStoreStub) uuidOf(name string) pgtype.UUID {
	if existing, ok := s.uuidByName[name]; ok {
		return existing
	}
	hex := "00000000-0000-0000-0000-0000000000" + paddedHex(len(s.uuidByName)+1)
	var id pgtype.UUID
	if err := id.Scan(hex); err != nil {
		s.t.Fatalf("uuid: %v", err)
	}
	s.uuidByName[name] = id
	return id
}

func paddedHex(value int) string {
	const digits = "0123456789abcdef"
	if value <= 0 {
		return "01"
	}
	high := value / 16
	low := value % 16
	return string([]byte{digits[high], digits[low]})
}

func formatUUID(value pgtype.UUID) string {
	bytes := value.Bytes
	return formatBytes(bytes[:])
}

func formatBytes(value []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, b := range value {
		out = append(out, digits[b>>4], digits[b&0x0f])
		if i == 3 || i == 5 || i == 7 || i == 9 {
			out = append(out, '-')
		}
	}
	return string(out)
}

func (s *jobStoreStub) put(name string, job sqlc.Job) {
	s.jobs[name] = job
}

func (s *jobStoreStub) snapshot(name string) sqlc.Job {
	job, ok := s.jobs[name]
	if !ok {
		s.t.Fatalf("job %q not found", name)
	}
	return job
}

func (s *jobStoreStub) findByUUID(id pgtype.UUID) (string, sqlc.Job) {
	for name, job := range s.jobs {
		if job.ID == id {
			return name, job
		}
	}
	s.t.Fatalf("job uuid %s not found", id)
	return "", sqlc.Job{}
}

func (s *jobStoreStub) GetJob(_ context.Context, id pgtype.UUID) (sqlc.Job, error) {
	_, job := s.findByUUID(id)
	return job, nil
}

func (s *jobStoreStub) MarkJobRunning(_ context.Context, arg sqlc.MarkJobRunningParams) (sqlc.Job, error) {
	s.markRunningCalls++
	name, job := s.findByUUID(arg.ID)
	job.Status = domain.JobStatusRunning
	job.LockedBy = arg.LockedBy
	job.Attempts++
	s.jobs[name] = job
	return job, nil
}

func (s *jobStoreStub) MarkJobSucceeded(_ context.Context, id pgtype.UUID) (sqlc.Job, error) {
	name, job := s.findByUUID(id)
	job.Status = domain.JobStatusSucceeded
	s.jobs[name] = job
	return job, nil
}

func (s *jobStoreStub) MarkJobFailed(_ context.Context, arg sqlc.MarkJobFailedParams) (sqlc.Job, error) {
	name, job := s.findByUUID(arg.ID)
	job.Status = domain.JobStatusFailed
	job.LastError = arg.LastError
	s.jobs[name] = job
	return job, nil
}

func (s *jobStoreStub) RetryJob(_ context.Context, arg sqlc.RetryJobParams) (sqlc.Job, error) {
	name, job := s.findByUUID(arg.ID)
	job.Status = domain.JobStatusPending
	job.RunAfter = arg.RunAfter
	job.LastError = arg.LastError
	s.jobs[name] = job
	return job, nil
}
