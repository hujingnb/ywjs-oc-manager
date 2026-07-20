// Package worker 的 worker_test 覆盖 worker 对 job 状态推进、重试和队列确认的处理。
package worker

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
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

// TestWorkerProcessJobID_DoesNotOverwriteReclaimedJob 覆盖旧 worker 在任务已被新 owner 接管后完成，不能覆盖新 owner 的 running 状态。
func TestWorkerProcessJobID_DoesNotOverwriteReclaimedJob(t *testing.T) {
	store := newJobStoreStub(t)
	registry := handlers.NewRegistry()
	registry.MustRegister("reclaimed", func(_ context.Context, job sqlc.Job) error {
		// 模拟旧 owner 的 lease 过期后被 recovery 回收、新 worker 用不同 token 重新领取。
		name, current := store.findByID(job.ID)
		current.Status = domain.JobStatusRunning
		current.LockedBy = null.StringFrom("worker-new")
		current.LeaseToken = null.StringFrom("lease-new")
		store.jobs[name] = current
		return nil
	})
	store.put("job-1", sqlc.Job{ID: store.id("job-1"), Type: "reclaimed", Status: domain.JobStatusPending, MaxAttempts: 3})

	// 旧 worker 的成功写入必须返回 stale-owner 错误，且数据库状态保留给新 owner。
	err := New(store, &queueStub{ids: []string{store.id("job-1")}}, registry, Config{WorkerID: "worker-old"}).processJobID(context.Background(), store.id("job-1"))

	require.ErrorIs(t, err, ErrStaleJobOwner)
	current := store.snapshot("job-1")
	assert.Equal(t, domain.JobStatusRunning, current.Status)
	assert.Equal(t, "worker-new", current.LockedBy.String)
	assert.Equal(t, "lease-new", current.LeaseToken.String)
}

// TestWorkerProcessJobID_RenewsLeaseDuringLongHandler 覆盖长时间 handler 持续续租，避免 recovery 把仍在执行的任务误判为遗留锁。
func TestWorkerProcessJobID_RenewsLeaseDuringLongHandler(t *testing.T) {
	store := newJobStoreStub(t)
	registry := handlers.NewRegistry()
	releaseHandler := make(chan struct{})
	registry.MustRegister("long-running", func(context.Context, sqlc.Job) error {
		<-releaseHandler
		return nil
	})
	store.put("job-1", sqlc.Job{ID: store.id("job-1"), Type: "long-running", Status: domain.JobStatusPending, MaxAttempts: 3})
	worker := New(store, &queueStub{}, registry, Config{WorkerID: "worker-1", LeaseRenewInterval: time.Millisecond})

	// handler 阻塞超过一个续租周期时，必须先刷新 locked_at；随后正常结束仍可完成。
	done := make(chan error, 1)
	go func() { done <- worker.processJobID(context.Background(), store.id("job-1")) }()
	require.Eventually(t, func() bool { return store.renewCalls.Load() > 0 }, time.Second, time.Millisecond)
	close(releaseHandler)
	require.NoError(t, <-done)
	assert.Equal(t, domain.JobStatusSucceeded, store.snapshot("job-1").Status)
}

// TestWorkerProcessJobID_CancelsHandlerWhenLeaseRenewalFails 覆盖续租未命中当前 owner 时，旧 handler 必须收到 context 取消并停止后续副作用。
func TestWorkerProcessJobID_CancelsHandlerWhenLeaseRenewalFails(t *testing.T) {
	store := newJobStoreStub(t)
	store.renewLeaseLost = true
	registry := handlers.NewRegistry()
	handlerCanceled := make(chan error, 1)
	registry.MustRegister("renew-failed", func(ctx context.Context, _ sqlc.Job) error {
		<-ctx.Done()
		handlerCanceled <- ctx.Err()
		return ctx.Err()
	})
	store.put("job-1", sqlc.Job{ID: store.id("job-1"), Type: "renew-failed", Status: domain.JobStatusPending, MaxAttempts: 3})

	// RenewJobLease 返回 0 等价于 owner 已不再匹配，worker 应取消 handler 且拒绝写入 retry/success 状态。
	err := New(store, &queueStub{}, registry, Config{WorkerID: "worker-1", LeaseRenewInterval: time.Millisecond}).processJobID(context.Background(), store.id("job-1"))

	require.ErrorIs(t, err, ErrStaleJobOwner)
	require.ErrorIs(t, <-handlerCanceled, context.Canceled)
	assert.Equal(t, domain.JobStatusRunning, store.snapshot("job-1").Status)
}

// TestWorkerProcessJobID_AllowsNewWorkerToTakeOverStaleOwner 覆盖 recovery 回收过期锁后，新 worker 能领取任务且旧 owner 不能回写结果。
func TestWorkerProcessJobID_AllowsNewWorkerToTakeOverStaleOwner(t *testing.T) {
	store := newJobStoreStub(t)
	oldRegistry := handlers.NewRegistry()
	oldStarted := make(chan struct{})
	releaseOld := make(chan struct{})
	oldRegistry.MustRegister("takeover", func(context.Context, sqlc.Job) error {
		close(oldStarted)
		<-releaseOld
		return nil
	})
	store.put("job-1", sqlc.Job{ID: store.id("job-1"), Type: "takeover", Status: domain.JobStatusPending, MaxAttempts: 3})
	oldWorker := New(store, &queueStub{}, oldRegistry, Config{WorkerID: "worker-old"})
	oldDone := make(chan error, 1)
	go func() { oldDone <- oldWorker.processJobID(context.Background(), store.id("job-1")) }()
	<-oldStarted

	// 模拟 recovery 清除旧 lease 后的新 worker 重新领取并成功完成任务。
	name, recovered := store.findByID(store.id("job-1"))
	recovered.Status = domain.JobStatusPending
	recovered.LockedBy = null.String{}
	recovered.LeaseToken = null.String{}
	store.jobs[name] = recovered
	newRegistry := handlers.NewRegistry()
	newRegistry.MustRegister("takeover", func(context.Context, sqlc.Job) error { return nil })
	require.NoError(t, New(store, &queueStub{}, newRegistry, Config{WorkerID: "worker-new"}).processJobID(context.Background(), store.id("job-1")))

	close(releaseOld)
	require.ErrorIs(t, <-oldDone, ErrStaleJobOwner)
	assert.Equal(t, domain.JobStatusSucceeded, store.snapshot("job-1").Status)
}

// TestWorkerProcessJobID_DoesNotExecuteExhaustedRecoveredJob 覆盖过期锁恢复后 attempts 已达上限的任务不能再次执行 handler。
func TestWorkerProcessJobID_DoesNotExecuteExhaustedRecoveredJob(t *testing.T) {
	store := newJobStoreStub(t)
	registry := handlers.NewRegistry()
	calls := 0
	registry.MustRegister("exhausted", func(context.Context, sqlc.Job) error {
		calls++
		return nil
	})
	store.put("job-1", sqlc.Job{ID: store.id("job-1"), Type: "exhausted", Status: domain.JobStatusPending, Attempts: 3, MaxAttempts: 3})

	// 领取 SQL 必须拒绝 attempts 已耗尽的 pending job，避免 stale recovery 额外执行一次业务副作用。
	require.NoError(t, New(store, &queueStub{}, registry, Config{WorkerID: "worker-1"}).processJobID(context.Background(), store.id("job-1")))
	assert.Zero(t, calls)
	assert.Equal(t, domain.JobStatusPending, store.snapshot("job-1").Status)
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

// TestWorkerTickCompensatesPromptFailureAfterMaxAttempts 验证成功前回调持续失败直至终态后，
// 失败补偿仅执行一次；原 job 保持 failed，不会被补偿回调重新循环。
func TestWorkerTickCompensatesPromptFailureAfterMaxAttempts(t *testing.T) {
	store := newJobStoreStub(t)
	registry := handlers.NewRegistry()
	registry.MustRegister("prompt-rollout", func(context.Context, sqlc.Job) error { return nil })
	require.NoError(t, registry.RegisterBeforeSuccess("prompt-rollout", func(context.Context, sqlc.Job) error { return errors.New("后继失败") }))
	compensations := 0
	require.NoError(t, registry.RegisterAfterFailure("prompt-rollout", func(context.Context, sqlc.Job) error { compensations++; return nil }))
	store.put("job-1", sqlc.Job{ID: store.id("job-1"), Type: "prompt-rollout", Status: domain.JobStatusPending, MaxAttempts: 1})

	require.NoError(t, New(store, &queueStub{ids: []string{store.id("job-1")}}, registry, Config{WorkerID: "w1"}).Tick(context.Background()))
	assert.Equal(t, domain.JobStatusFailed, store.snapshot("job-1").Status)
	assert.Equal(t, 1, compensations)
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
	renewCalls       atomic.Int32
	renewLeaseLost   bool
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

// MarkJobRunning 仅领取 pending job，并保存随机 lease token。
func (s *jobStoreStub) MarkJobRunning(_ context.Context, arg sqlc.MarkJobRunningParams) (int64, error) {
	s.markRunningCalls++
	name, job := s.findByID(arg.ID)
	if job.Status != domain.JobStatusPending || job.Attempts >= job.MaxAttempts {
		return 0, nil
	}
	job.Status = domain.JobStatusRunning
	job.LockedBy = arg.LockedBy
	job.LeaseToken = arg.LeaseToken
	job.Attempts++
	s.jobs[name] = job
	return 1, nil
}

// MarkJobSucceeded 仅允许当前 token 所属 owner 写入终态。
func (s *jobStoreStub) MarkJobSucceeded(_ context.Context, arg sqlc.MarkJobSucceededParams) (int64, error) {
	name, job := s.findByID(arg.ID)
	if job.Status != domain.JobStatusRunning || job.LockedBy != arg.LockedBy || job.LeaseToken != arg.LeaseToken {
		return 0, nil
	}
	job.Status = domain.JobStatusSucceeded
	s.jobs[name] = job
	return 1, nil
}

// MarkJobFailed 仅允许当前 token 所属 owner 写入终态。
func (s *jobStoreStub) MarkJobFailed(_ context.Context, arg sqlc.MarkJobFailedParams) (int64, error) {
	s.markFailedCalls++
	name, job := s.findByID(arg.ID)
	if job.Status != domain.JobStatusRunning || job.LockedBy != arg.LockedBy || job.LeaseToken != arg.LeaseToken {
		return 0, nil
	}
	job.Status = domain.JobStatusFailed
	job.LastError = arg.LastError
	s.jobs[name] = job
	return 1, nil
}

// RetryJob 仅允许当前 token 所属 owner 释放任务并设退避时间。
func (s *jobStoreStub) RetryJob(_ context.Context, arg sqlc.RetryJobParams) (int64, error) {
	s.retryCalls++
	name, job := s.findByID(arg.ID)
	if job.Status != domain.JobStatusRunning || job.LockedBy != arg.LockedBy || job.LeaseToken != arg.LeaseToken {
		return 0, nil
	}
	job.Status = domain.JobStatusPending
	job.RunAfter = arg.RunAfter
	job.LastError = arg.LastError
	s.jobs[name] = job
	return 1, nil
}

// DeferJob 抵消本次领取增加的 attempts，并按指定时间把任务退回 pending。
func (s *jobStoreStub) DeferJob(_ context.Context, arg sqlc.DeferJobParams) (int64, error) {
	s.deferCalls++
	name, job := s.findByID(arg.ID)
	if job.Status != domain.JobStatusRunning || job.LockedBy != arg.LockedBy || job.LeaseToken != arg.LeaseToken {
		return 0, nil
	}
	job.Status = domain.JobStatusPending
	job.RunAfter = arg.RunAfter
	if job.Attempts > 0 {
		job.Attempts--
	}
	job.LastError = null.String{}
	job.LockedBy = null.String{}
	job.LeaseToken = null.String{}
	s.jobs[name] = job
	return 1, nil
}

// RenewJobLease 仅在当前 owner 未被接管时刷新 lease；测试桩无需存储真实时间。
func (s *jobStoreStub) RenewJobLease(_ context.Context, arg sqlc.RenewJobLeaseParams) (int64, error) {
	_, job := s.findByID(arg.ID)
	if job.Status != domain.JobStatusRunning || job.LockedBy != arg.LockedBy || job.LeaseToken != arg.LeaseToken {
		return 0, nil
	}
	s.renewCalls.Add(1)
	if s.renewLeaseLost {
		return 0, nil
	}
	return 1, nil
}
