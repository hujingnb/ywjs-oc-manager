// Package aicc 覆盖 AICC 后台留存清理任务的调度边界。
package aicc

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	null "github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ocredis "oc-manager/internal/redis"
	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// TestRetentionLoopTickOnceCleansExpiredSession 覆盖单轮调度：获取分布式锁后执行保留期清理，
// 清理结束必须释放同一 token 的锁，防止后续周期永久跳过。
func TestRetentionLoopTickOnceCleansExpiredSession(t *testing.T) {
	store := &retentionStore{sessions: []sqlc.AiccSession{{ID: "session-1", OrgID: "org-1"}}}
	locker := &retentionLocker{acquired: true}
	loop := NewRetentionLoop(service.NewAICCRetentionService(store, nil), locker, "worker-1", slog.Default())

	loop.tickOnce(context.Background())

	require.Equal(t, int32(100), store.limit)
	assert.Equal(t, []string{"session-1"}, store.deleted)
	require.NotEmpty(t, locker.acquireToken)
	assert.Equal(t, locker.acquireToken, locker.releaseToken)
}

// TestRetentionLoopStartTriggersPeriodicCleanup 覆盖启动后的周期调度：首轮没有过期数据时，
// 后续出现的过期会话仍须在下一轮扫描中被清理，避免仅验证启动时的一次性执行。
func TestRetentionLoopStartTriggersPeriodicCleanup(t *testing.T) {
	store := &retentionStore{
		sessions:            []sqlc.AiccSession{{ID: "session-after-start", OrgID: "org-1"}},
		returnSessionsAfter: 2,
	}
	locker := &retentionLocker{acquired: true}
	loop := NewRetentionLoop(service.NewAICCRetentionService(store, nil), locker, "worker-1", slog.Default())
	loop.tick = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx)

	require.Eventually(t, func() bool {
		return store.deletedCount() == 1
	}, time.Second, 10*time.Millisecond)
	assert.GreaterOrEqual(t, store.callCount(), 2)
}

// retentionStore 仅实现留存任务所需的数据访问面，记录清理链路的关键调用。
type retentionStore struct {
	mu                  sync.Mutex
	sessions            []sqlc.AiccSession
	returnSessionsAfter int
	calls               int
	limit               int32
	deleted             []string
}

func (s *retentionStore) ListExpiredAICCSessions(_ context.Context, limit int32) ([]sqlc.AiccSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.limit = limit
	if s.returnSessionsAfter > 0 && s.calls < s.returnSessionsAfter {
		return nil, nil
	}
	return s.sessions, nil
}

func (*retentionStore) ListAICCImageObjectKeysBySession(context.Context, string) ([]string, error) { return nil, nil }
func (*retentionStore) ClearAICCLeadLatestSession(context.Context, null.String) error { return nil }
func (*retentionStore) DeleteOrphanAICCLeadsByOrg(context.Context, string) error { return nil }
func (s *retentionStore) DeleteAICCSession(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, id)
	return nil
}

// deletedCount 以锁保护异步断言读取，避免测试自身产生数据竞争。
func (s *retentionStore) deletedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.deleted)
}

// callCount 返回后台扫描次数，用于断言清理来自启动后的周期而非首轮。
func (s *retentionStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// retentionLocker 模拟互斥锁，确保测试可精确比对 acquire/release 使用的 token。
type retentionLocker struct {
	acquired     bool
	acquireToken string
	releaseToken string
}

func (l *retentionLocker) TryAcquire(_ context.Context, _ string, token string, _ time.Duration) (bool, error) {
	l.acquireToken = token
	return l.acquired, nil
}
func (*retentionLocker) Refresh(context.Context, string, string, time.Duration) (bool, error) { return false, nil }
func (l *retentionLocker) Release(_ context.Context, _ string, token string) error {
	l.releaseToken = token
	return nil
}
func (*retentionLocker) Exists(context.Context, string) (bool, error) { return false, nil }

var _ ocredis.DistLocker = (*retentionLocker)(nil)
