package service

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resourceSampleCleanupStoreStub 记录清理器传入的 cutoff 和批大小，避免测试依赖真实数据库。
type resourceSampleCleanupStoreStub struct {
	// nodeCutoff 记录节点资源采样删除时使用的过期时间边界。
	nodeCutoff pgtype.Timestamptz
	// nodeLimit 记录节点资源采样删除时使用的单批上限。
	nodeLimit int32
	// instanceCutoff 记录实例资源采样删除时使用的过期时间边界。
	instanceCutoff pgtype.Timestamptz
	// instanceLimit 记录实例资源采样删除时使用的单批上限。
	instanceLimit int32
}

// DeleteOldNodeResourceSamples 模拟节点采样删除，并保存调用参数供断言使用。
func (s *resourceSampleCleanupStoreStub) DeleteOldNodeResourceSamples(_ context.Context, cutoff pgtype.Timestamptz, limit int32) (int64, error) {
	s.nodeCutoff = cutoff
	s.nodeLimit = limit
	return 12, nil
}

// DeleteOldInstanceResourceSamples 模拟实例采样删除，并保存调用参数供断言使用。
func (s *resourceSampleCleanupStoreStub) DeleteOldInstanceResourceSamples(_ context.Context, cutoff pgtype.Timestamptz, limit int32) (int64, error) {
	s.instanceCutoff = cutoff
	s.instanceLimit = limit
	return 34, nil
}

// TestResourceSampleCleanupDeletesOldSamplesInBatches 验证清理任务按固定保留期和批量上限删除两类资源采样。
func TestResourceSampleCleanupDeletesOldSamplesInBatches(t *testing.T) {
	fixedNow := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	expectedCutoff := fixedNow.Add(-30 * 24 * time.Hour)
	store := &resourceSampleCleanupStoreStub{}
	cleanup := NewResourceSampleCleanup(store)
	cleanup.SetClock(func() time.Time { return fixedNow })

	nodeDeleted, instanceDeleted, err := cleanup.RunOnce(context.Background())

	require.NoError(t, err)
	assert.Equal(t, int64(12), nodeDeleted)
	assert.Equal(t, int64(34), instanceDeleted)
	assert.True(t, store.nodeCutoff.Valid)
	assert.True(t, store.instanceCutoff.Valid)
	assert.Equal(t, expectedCutoff, store.nodeCutoff.Time)
	assert.Equal(t, expectedCutoff, store.instanceCutoff.Time)
	assert.Equal(t, int32(1000), store.nodeLimit)
	assert.Equal(t, int32(1000), store.instanceLimit)
}
