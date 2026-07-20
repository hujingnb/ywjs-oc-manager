// Package worker 的 worker_test 覆盖 worker 对 job 状态推进、重试和队列确认的处理。
package worker

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	null "github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
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
	// ID 现为 string；store.id("job-1") 返回 string 形式的 UUID。
	store.put("job-1", sqlc.Job{ID: store.id("job-1"), Type: "noop", Status: domain.JobStatusPending, MaxAttempts: 3})

	w := New(store, queue, registry, Config{WorkerID: "w1"})
	err := w.Tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, domain.JobStatusSucceeded, store.snapshot("job-1").Status)
}

// TestWorkerTickRunsSuccessCallbackBeforeSucceeded 验证后继调度在成功前运行；回调失败可沿当前 job 重试。
func TestWorkerTickRunsSuccessCallbackBeforeSucceeded(t *testing.T) {
	store := newJobStoreStub(t)
	registry := handlers.NewRegistry()
	registry.MustRegister("prompt-rollout", func(context.Context, sqlc.Job) error { return nil })
	callbackSawRunning := false
	require.NoError(t, registry.RegisterBeforeSuccess("prompt-rollout", func(_ context.Context, job sqlc.Job) error {
		_, current := store.findByID(job.ID)
		callbackSawRunning = current.Status == domain.JobStatusRunning
		return nil
	}))
	store.put("job-1", sqlc.Job{ID: store.id("job-1"), Type: "prompt-rollout", Status: domain.JobStatusPending, MaxAttempts: 3})

	require.NoError(t, New(store, &queueStub{ids: []string{store.id("job-1")}}, registry, Config{WorkerID: "w1"}).Tick(context.Background()))
	assert.True(t, callbackSawRunning)
}

// TestWorkerTickRetriesBeforeSuccessCallbackFailure 验证成功前后继调度首次失败时旧任务保持 retryable；
// 下一次重试成功后仅创建一次 successor，且旧任务才进入 succeeded。
func TestWorkerTickRetriesBeforeSuccessCallbackFailure(t *testing.T) {
	store := newJobStoreStub(t)
	registry := handlers.NewRegistry()
	registry.MustRegister("prompt-rollout", func(context.Context, sqlc.Job) error { return nil })
	callbackCalls, successors := 0, 0
	require.NoError(t, registry.RegisterBeforeSuccess("prompt-rollout", func(context.Context, sqlc.Job) error {
		callbackCalls++
		if callbackCalls == 1 {
			return errors.New("后继任务写入失败")
		}
		successors++
		return nil
	}))
	store.put("job-1", sqlc.Job{ID: store.id("job-1"), Type: "prompt-rollout", Status: domain.JobStatusPending, MaxAttempts: 3})
	queue := &queueStub{ids: []string{store.id("job-1")}}
	worker := New(store, queue, registry, Config{WorkerID: "w1", BackoffBase: time.Millisecond})

	require.NoError(t, worker.Tick(context.Background()))
	assert.Equal(t, domain.JobStatusPending, store.snapshot("job-1").Status)
	assert.Zero(t, successors)

	queue.ids = []string{store.id("job-1")}
	require.NoError(t, worker.Tick(context.Background()))
	assert.Equal(t, domain.JobStatusSucceeded, store.snapshot("job-1").Status)
	assert.Equal(t, 1, successors)
}

// TestWorkerTickRetriesUntilMaxAttempts 验证workerTickRetriesUntil最大Attempts的边界条件场景。
func TestWorkerTickRetriesUntilMaxAttempts(t *testing.T) {
	store := newJobStoreStub(t)
	registry := handlers.NewRegistry()
	registry.MustRegister("flaky", func(_ context.Context, _ sqlc.Job) error { return errors.New("boom") })
	queue := &queueStub{ids: []string{store.id("job-1")}}
	// ID 现为 string；store.id("job-1") 返回字符串 UUID。
	store.put("job-1", sqlc.Job{ID: store.id("job-1"), Type: "flaky", Status: domain.JobStatusPending, MaxAttempts: 2})

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w := New(store, queue, registry, Config{WorkerID: "w1", BackoffBase: time.Second, BackoffFactor: 2, BackoffMax: 10 * time.Second})
	w.SetClock(func() time.Time { return now })

	err := w.Tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, domain.JobStatusPending, store.snapshot("job-1").Status)
	// RunAfter 现为 time.Time；通过 Sub 计算退避间隔。
	require.Equal(t, time.Second, store.snapshot("job-1").RunAfter.Sub(now))

	// 第二轮：重新入队，把状态还原为 pending（模拟 reaper/scheduler 重置后再次被 worker 拾取）。
	queue.ids = []string{store.id("job-1")}
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
	// ID 现为 string。
	store.put("job-1", sqlc.Job{ID: store.id("job-1"), Type: "noop", Status: domain.JobStatusRunning, MaxAttempts: 1})

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
	// ID 现为 string。
	store.put("job-1", sqlc.Job{ID: store.id("job-1"), Type: "missing", Status: domain.JobStatusPending, MaxAttempts: 3})

	w := New(store, queue, registry, Config{WorkerID: "w1"})
	err := w.Tick(context.Background())
	require.NoError(t, err)
	require.Equal(t, domain.JobStatusFailed, store.snapshot("job-1").Status)
}

// TestWorkerTickDefersWithoutConsumingAttempt 验证 handler 请求 defer 时，worker 释放槽位并把任务
// 原样退回 pending：不走普通 RetryJob/MarkJobFailed，且抵消 MarkJobRunning 增加的 attempts。
func TestWorkerTickDefersWithoutConsumingAttempt(t *testing.T) {
	store := newJobStoreStub(t)
	registry := handlers.NewRegistry()
	registry.MustRegister("deferred", func(context.Context, sqlc.Job) error {
		return &handlers.DeferredJobError{Delay: 2 * time.Second, Reason: "等待同企业旧任务"}
	})
	queue := &queueStub{ids: []string{store.id("job-1")}}
	store.put("job-1", sqlc.Job{ID: store.id("job-1"), Type: "deferred", Status: domain.JobStatusPending, Attempts: 1, MaxAttempts: 2, LastError: null.StringFrom("旧错误")})
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	worker := New(store, queue, registry, Config{WorkerID: "w1"})
	worker.SetClock(func() time.Time { return now })

	require.NoError(t, worker.Tick(context.Background()))
	job := store.snapshot("job-1")
	assert.Equal(t, domain.JobStatusPending, job.Status)
	assert.Equal(t, int32(1), job.Attempts)
	assert.Equal(t, now.Add(2*time.Second), job.RunAfter)
	assert.False(t, job.LastError.Valid)
	assert.Equal(t, 1, store.deferCalls)
	assert.Zero(t, store.retryCalls)
	assert.Zero(t, store.markFailedCalls)
}

type queueStub struct {
	ids []string
}

func (q *queueStub) Reserve(_ context.Context, _ int) ([]string, error) {
	out := q.ids
	q.ids = nil
	return out, nil
}

// jobStoreStub 实现 JobStore 接口。
// 迁移后 ID 为 string（MySQL uuid），所有 MarkJob*/RetryJob 仅返回 error（:exec 语义）。
type jobStoreStub struct {
	t                *testing.T
	jobs             map[string]sqlc.Job // 以 name 为 key 存储 job
	idByName         map[string]string   // name → string uuid
	markRunningCalls int
	deferCalls       int
	retryCalls       int
	markFailedCalls  int
}

func newJobStoreStub(t *testing.T) *jobStoreStub {
	return &jobStoreStub{
		t:        t,
		jobs:     map[string]sqlc.Job{},
		idByName: map[string]string{},
	}
}

// id 返回 name 对应的字符串 UUID；同名多次调用返回相同值。
func (s *jobStoreStub) id(name string) string {
	if existing, ok := s.idByName[name]; ok {
		return existing
	}
	// 以序号生成固定格式 UUID，确保可重现。
	n := len(s.idByName) + 1
	uid := fmt.Sprintf("00000000-0000-0000-0000-0000000000%02x", n)
	s.idByName[name] = uid
	return uid
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

// findByID 根据字符串 UUID 找到 job 所在的 name 和 job；未找到时让测试立即失败。
func (s *jobStoreStub) findByID(id string) (string, sqlc.Job) {
	for name, job := range s.jobs {
		if job.ID == id {
			return name, job
		}
	}
	s.t.Fatalf("job id %s not found", id)
	return "", sqlc.Job{}
}

// GetJob 按字符串 UUID 查 job；JobStore 接口迁移后参数为 string。
func (s *jobStoreStub) GetJob(_ context.Context, id string) (sqlc.Job, error) {
	_, job := s.findByID(id)
	return job, nil
}

// MarkJobRunning 更新 job 状态为 running；:exec 语义仅返回 error。
func (s *jobStoreStub) MarkJobRunning(_ context.Context, arg sqlc.MarkJobRunningParams) error {
	s.markRunningCalls++
	name, job := s.findByID(arg.ID)
	job.Status = domain.JobStatusRunning
	job.LockedBy = arg.LockedBy
	job.Attempts++
	s.jobs[name] = job
	return nil
}

// MarkJobSucceeded 更新 job 状态为 succeeded；:exec 语义仅返回 error。
func (s *jobStoreStub) MarkJobSucceeded(_ context.Context, id string) error {
	name, job := s.findByID(id)
	job.Status = domain.JobStatusSucceeded
	s.jobs[name] = job
	return nil
}

// MarkJobFailed 更新 job 状态为 failed；:exec 语义仅返回 error。
func (s *jobStoreStub) MarkJobFailed(_ context.Context, arg sqlc.MarkJobFailedParams) error {
	s.markFailedCalls++
	name, job := s.findByID(arg.ID)
	job.Status = domain.JobStatusFailed
	job.LastError = arg.LastError
	s.jobs[name] = job
	return nil
}

// RetryJob 把 job 重置回 pending 并设退避时间；:exec 语义仅返回 error。
func (s *jobStoreStub) RetryJob(_ context.Context, arg sqlc.RetryJobParams) error {
	s.retryCalls++
	name, job := s.findByID(arg.ID)
	job.Status = domain.JobStatusPending
	job.RunAfter = arg.RunAfter
	job.LastError = arg.LastError
	s.jobs[name] = job
	return nil
}

// DeferJob 抵消本次领取增加的 attempts，并按指定时间把任务退回 pending。
func (s *jobStoreStub) DeferJob(_ context.Context, arg sqlc.DeferJobParams) (int64, error) {
	s.deferCalls++
	name, job := s.findByID(arg.ID)
	job.Status = domain.JobStatusPending
	job.RunAfter = arg.RunAfter
	if job.Attempts > 0 {
		job.Attempts--
	}
	job.LastError = null.String{}
	job.LockedBy = null.String{}
	s.jobs[name] = job
	return 1, nil
}
