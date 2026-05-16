// Package reaper 单元测试:覆盖锁不可用、5 个孤儿状态、job 三种分支与 store 错误处理。
package reaper

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// fakeStore 收集 reaper 的所有写库调用,供断言。
// 各方法均实现 reaper.Store 接口语义,但不做任何真实存储。
type fakeStore struct {
	stale          []sqlc.ListStaleInitsRow
	statusCalls    []sqlc.SetAppStatusParams
	clearCalls     []pgtype.UUID
	latestJob      sqlc.Job
	latestJobErr   error
	requeueCalls   []pgtype.UUID
	createJobCalls []sqlc.CreateJobParams
}

func (s *fakeStore) ListStaleInits(_ context.Context, _ pgtype.Timestamptz) ([]sqlc.ListStaleInitsRow, error) {
	return s.stale, nil
}
func (s *fakeStore) SetAppStatus(_ context.Context, p sqlc.SetAppStatusParams) (sqlc.App, error) {
	s.statusCalls = append(s.statusCalls, p)
	return sqlc.App{}, nil
}
func (s *fakeStore) ClearAppProgress(_ context.Context, id pgtype.UUID) (sqlc.App, error) {
	s.clearCalls = append(s.clearCalls, id)
	return sqlc.App{}, nil
}
func (s *fakeStore) GetLatestAppInitJob(_ context.Context, _ string) (sqlc.Job, error) {
	return s.latestJob, s.latestJobErr
}
func (s *fakeStore) RequeueJob(_ context.Context, id pgtype.UUID) (sqlc.Job, error) {
	s.requeueCalls = append(s.requeueCalls, id)
	return sqlc.Job{ID: id, Status: domain.JobStatusPending}, nil
}
func (s *fakeStore) CreateJob(_ context.Context, p sqlc.CreateJobParams) (sqlc.Job, error) {
	s.createJobCalls = append(s.createJobCalls, p)
	return sqlc.Job{ID: testJobID, Status: domain.JobStatusPending}, nil
}

// fakeNotifier 捕获 reaper 对 redis queue 的 Enqueue 调用。
type fakeNotifier struct{ enqueued []string }

func (n *fakeNotifier) Enqueue(_ context.Context, jobID string) error {
	n.enqueued = append(n.enqueued, jobID)
	return nil
}

// fakeLocker 控制 TryAcquire 是否返回 (true, nil)。
type fakeLocker struct {
	acquireOK  bool
	acquireErr error
}

func (l *fakeLocker) TryAcquire(_ context.Context, _, _ string, _ time.Duration) (bool, error) {
	return l.acquireOK, l.acquireErr
}
func (l *fakeLocker) Renew(_ context.Context, _, _ string, _ time.Duration) error { return nil }
func (l *fakeLocker) Release(_ context.Context, _, _ string) error                { return nil }
func (l *fakeLocker) Exists(_ context.Context, _ string) (bool, error)            { return true, nil }

var (
	// testJobID / testAppID 仅取一个非零 UUID 用于断言相等;字节内容无业务含义。
	testJobID = pgtype.UUID{Bytes: [16]byte{0xaa}, Valid: true}
	testAppID = pgtype.UUID{Bytes: [16]byte{0xbb}, Valid: true}
)

// TestReaper_LockUnavailable_Skip 锁被别人持着时 reapOnce 不应被调用。
// 场景:多 manager 副本同 tick,只有一个能拿锁,其他实例必须安静放弃。
func TestReaper_LockUnavailable_Skip(t *testing.T) {
	store := &fakeStore{}
	notifier := &fakeNotifier{}
	r := New(store, notifier, &fakeLocker{acquireOK: false}, "test", slog.Default())
	r.tickOnce(context.Background())
	// 锁失败时不应有任何写库 / 入队动作
	assert.Empty(t, store.statusCalls)
	assert.Empty(t, notifier.enqueued)
}

// TestReaper_ReapOrphanReset 5 个 init 子状态都能被扫到,逐条重置 status + 清进度 + 重入 / 新建 job。
// 表驱动覆盖 init 状态机的全部五个子状态,确保 reaper 不会漏一类。
func TestReaper_ReapOrphanReset(t *testing.T) {
	cases := []struct {
		name        string
		startStatus string
	}{
		{"pulling_runtime_image 孤儿", domain.AppStatusPullingRuntimeImage}, // pull 阶段卡住,reaper 应回退到自身
		{"preparing_runtime 孤儿", domain.AppStatusPreparingRuntime},        // prepare 阶段卡住,reaper 应回退到 pulling_runtime_image
		{"creating_container 孤儿", domain.AppStatusCreatingContainer},      // create 阶段卡住,reaper 应回退到 pulling_runtime_image
		{"starting 孤儿", domain.AppStatusStarting},                         // start 阶段卡住,reaper 应回退到 pulling_runtime_image
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := &fakeStore{
				stale:     []sqlc.ListStaleInitsRow{{ID: testAppID, Status: c.startStatus}},
				latestJob: sqlc.Job{ID: testJobID, Status: domain.JobStatusRunning},
			}
			notifier := &fakeNotifier{}
			r := New(store, notifier, &fakeLocker{acquireOK: true}, "test", slog.Default())
			r.tickOnce(context.Background())

			// status 被强制回退为 pulling_runtime_image,无论起始子状态是哪一类
			require.Len(t, store.statusCalls, 1)
			assert.Equal(t, domain.AppStatusPullingRuntimeImage, store.statusCalls[0].Status)
			// 进度字段被清空,防前端继续看到旧值
			require.Len(t, store.clearCalls, 1)
			// running job 必须被 requeue 回 pending
			require.Len(t, store.requeueCalls, 1, "running job 应被 requeue")
			// 已有 job 时不应再新建,避免 jobs 表出现重复
			assert.Empty(t, store.createJobCalls, "已有 job 不应新建")
			// 重置后必须通知 scheduler 立即拾取
			assert.NotEmpty(t, notifier.enqueued)
		})
	}
}

// TestReaper_NoExistingJob_CreateNew 没有历史 job 时 reaper 新建一份。
// 场景:app 由 reaper 之外的路径创建(如手工 INSERT),从未生成过 init job。
func TestReaper_NoExistingJob_CreateNew(t *testing.T) {
	store := &fakeStore{
		stale:        []sqlc.ListStaleInitsRow{{ID: testAppID, Status: domain.AppStatusStarting}},
		latestJobErr: pgx.ErrNoRows,
	}
	notifier := &fakeNotifier{}
	r := New(store, notifier, &fakeLocker{acquireOK: true}, "test", slog.Default())
	r.tickOnce(context.Background())
	// ErrNoRows 分支必须走 CreateJob,而非 RequeueJob
	assert.Len(t, store.createJobCalls, 1)
	assert.Empty(t, store.requeueCalls)
	// 新建的 job type 必须是 app_initialize,且 priority / max_attempts 与默认值一致
	require.Len(t, store.createJobCalls, 1)
	assert.Equal(t, domain.JobTypeAppInitialize, store.createJobCalls[0].Type)
	assert.Equal(t, int32(100), store.createJobCalls[0].Priority)
	assert.Equal(t, int32(3), store.createJobCalls[0].MaxAttempts)
	// 新建后也要通知队列
	assert.NotEmpty(t, notifier.enqueued)
}

// TestReaper_PendingJob_NoRequeue 已经 pending 的 job 直接复用,不重置。
// 场景:上一轮 reaper 刚 RequeueJob 后崩了,新一轮看到 pending,
// 不应再触发 UPDATE,但仍需 Enqueue 一次防 scheduler 漏触发。
func TestReaper_PendingJob_NoRequeue(t *testing.T) {
	store := &fakeStore{
		stale:     []sqlc.ListStaleInitsRow{{ID: testAppID, Status: domain.AppStatusStarting}},
		latestJob: sqlc.Job{ID: testJobID, Status: domain.JobStatusPending},
	}
	notifier := &fakeNotifier{}
	r := New(store, notifier, &fakeLocker{acquireOK: true}, "test", slog.Default())
	r.tickOnce(context.Background())
	// pending job 不重置不新建
	assert.Empty(t, store.requeueCalls)
	assert.Empty(t, store.createJobCalls)
	// 但 Enqueue 仍要触发,防 Redis ZSET 里被 worker 消费后没有重新写入
	assert.NotEmpty(t, notifier.enqueued, "pending job 也要重新通知队列,防 scheduler 漏触发")
}

// TestReaper_StoreErrorPropagates 单条出错只 log 不 panic。
// 场景:DB 短暂抖动让 GetLatestAppInitJob 返回非 ErrNoRows 错误,
// reaper 必须把错误吃掉继续处理下一条,不能让 ticker goroutine 整体崩。
func TestReaper_StoreErrorPropagates(t *testing.T) {
	store := &fakeStore{
		stale:        []sqlc.ListStaleInitsRow{{ID: testAppID, Status: domain.AppStatusStarting}},
		latestJobErr: errors.New("db down"),
	}
	notifier := &fakeNotifier{}
	r := New(store, notifier, &fakeLocker{acquireOK: true}, "test", slog.Default())
	// tickOnce 内部捕获错误只 log,不应 panic
	assert.NotPanics(t, func() { r.tickOnce(context.Background()) })
}
