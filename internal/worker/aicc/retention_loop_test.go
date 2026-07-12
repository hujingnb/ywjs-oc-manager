// Package aicc 覆盖 AICC 后台留存清理任务的调度边界。
package aicc

import (
	"context"
	"log/slog"
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

// retentionStore 仅实现留存任务所需的数据访问面，记录清理链路的关键调用。
type retentionStore struct {
	sessions []sqlc.AiccSession
	limit    int32
	deleted  []string
}

func (s *retentionStore) ListExpiredAICCSessions(_ context.Context, limit int32) ([]sqlc.AiccSession, error) {
	s.limit = limit
	return s.sessions, nil
}

func (*retentionStore) ListAICCImageObjectKeysBySession(context.Context, string) ([]string, error) { return nil, nil }
func (*retentionStore) ClearAICCLeadLatestSession(context.Context, null.String) error { return nil }
func (*retentionStore) DeleteOrphanAICCLeadsByOrg(context.Context, string) error { return nil }
func (s *retentionStore) DeleteAICCSession(_ context.Context, id string) error {
	s.deleted = append(s.deleted, id)
	return nil
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
