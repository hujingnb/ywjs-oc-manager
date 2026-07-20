package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/guregu/null/v5"
	"oc-manager/internal/store/sqlc"
)

// TestJobRecoveryTick_RequeuesExpiredRunningJobs 覆盖进程异常退出后，超过租约的 running 任务恢复为 pending 的场景。
func TestJobRecoveryTick_RequeuesExpiredRunningJobs(t *testing.T) {
	store := &recoveryStore{recovered: 1}
	recovery := NewJobRecovery(store, &recoveryLocker{acquired: true}, "instance-1", JobRecoveryConfig{
		Lease: 5 * time.Minute,
	})
	recovery.SetClock(func() time.Time { return time.Date(2026, 7, 21, 1, 0, 0, 0, time.UTC) })

	// 超过五分钟的锁应被原子回收，恢复后的任务可由 scheduler 在下一轮扫描发现。
	recovered, err := recovery.Tick(context.Background())

	require.NoError(t, err)
	assert.Equal(t, int64(1), recovered)
	require.Len(t, store.thresholds, 1)
	assert.Equal(t, time.Date(2026, 7, 21, 0, 55, 0, 0, time.UTC), store.thresholds[0])
}

// TestJobRecoveryTick_DoesNotRequeueFreshOrUnlockedJobs 覆盖仍在安全租约内及没有锁时间的 running 任务不能被误回收的边界。
func TestJobRecoveryTick_DoesNotRequeueFreshOrUnlockedJobs(t *testing.T) {
	store := &recoveryStore{recovered: 0}
	recovery := NewJobRecovery(store, &recoveryLocker{acquired: true}, "instance-1", JobRecoveryConfig{
		Lease: 5 * time.Minute,
	})

	// SQL 的严格阈值筛选负责排除新鲜锁和 locked_at 为 NULL 的异常记录。
	recovered, err := recovery.Tick(context.Background())

	require.NoError(t, err)
	assert.Zero(t, recovered)
	assert.Len(t, store.thresholds, 1)
}

// TestJobRecoveryStart_RunsImmediately 覆盖服务启动时无需等待轮询间隔即可尝试回收遗留任务。
func TestJobRecoveryStart_RunsImmediately(t *testing.T) {
	store := &recoveryStore{}
	recovery := NewJobRecovery(store, &recoveryLocker{acquired: true}, "instance-1", JobRecoveryConfig{
		Interval: time.Hour,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	recovery.Start(ctx)

	require.Eventually(t, func() bool { return store.calls() == 1 }, time.Second, 10*time.Millisecond)
}

// TestJobRecoveryTick_MakesRecoveredJobDiscoverableByScheduler 覆盖过期锁回收后，下一次 scheduler 扫描能把任务重新投递给 worker 的链路。
func TestJobRecoveryTick_MakesRecoveredJobDiscoverableByScheduler(t *testing.T) {
	store := &recoveryStore{recovered: 1}
	recovery := NewJobRecovery(store, &recoveryLocker{acquired: true}, "instance-1", JobRecoveryConfig{})
	queue := &queueStub{}

	// recovery 先把遗留 running 任务置回 pending，scheduler 随后应发现并投递同一个任务 ID。
	_, err := recovery.Tick(context.Background())
	require.NoError(t, err)
	require.NoError(t, New(store, queue, Config{}).Tick(context.Background()))
	assert.Equal(t, []string{"recovered-job"}, queue.enqueued)
}

// recoveryStore 记录回收查询传入的阈值，避免单元测试依赖真实数据库。
type recoveryStore struct {
	mu         sync.Mutex
	thresholds []time.Time
	recovered  int64
	ready      []sqlc.Job
}

// RequeueExpiredRunningJobs 模拟 SQL 原子更新返回的影响行数。
func (s *recoveryStore) RequeueExpiredRunningJobs(_ context.Context, lockedBefore null.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.thresholds = append(s.thresholds, lockedBefore.Time)
	if s.recovered > 0 {
		s.ready = []sqlc.Job{{ID: "recovered-job"}}
	}
	return s.recovered, nil
}

// ListReadyJobs 模拟 scheduler 扫描已经恢复为 pending 的任务。
func (s *recoveryStore) ListReadyJobs(_ context.Context, _ int32) ([]sqlc.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sqlc.Job(nil), s.ready...), nil
}

// calls 返回已经执行的回收扫描次数。
func (s *recoveryStore) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.thresholds)
}

// recoveryLocker 模拟多副本互斥锁，只暴露本测试需要的抢锁与释放行为。
type recoveryLocker struct {
	acquired bool
}

// TryAcquire 返回预设结果。
func (l *recoveryLocker) TryAcquire(context.Context, string, string, time.Duration) (bool, error) {
	return l.acquired, nil
}

// Release 在测试中无需保存锁状态。
func (*recoveryLocker) Release(context.Context, string, string) error { return nil }

// Refresh 和 Exists 用于满足 Redis 分布式锁接口。
func (*recoveryLocker) Refresh(context.Context, string, string, time.Duration) (bool, error) {
	return false, nil
}
func (*recoveryLocker) Exists(context.Context, string) (bool, error) { return false, nil }

var _ interface {
	RequeueExpiredRunningJobs(context.Context, null.Time) (int64, error)
} = (*recoveryStore)(nil)
