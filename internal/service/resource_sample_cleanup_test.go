package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resourceSampleCleanupStoreStub 记录清理器传入的 cutoff 和批大小，避免测试依赖真实数据库。
// cutoff 现在是 time.Time（MySQL DATETIME），不再是 pgtype.Timestamptz。
type resourceSampleCleanupStoreStub struct {
	// nodeCutoff 记录节点资源采样删除时使用的过期时间边界。
	nodeCutoff time.Time
	// nodeCutoffs 记录节点资源采样每个批次的 cutoff，验证循环清理不会改变边界。
	nodeCutoffs []time.Time
	// nodeLimit 记录节点资源采样删除时使用的单批上限。
	nodeLimit int32
	// nodeLimits 记录节点资源采样每个批次的 limit，验证所有批次都使用固定上限。
	nodeLimits []int32
	// nodeDeletes 允许测试配置每个批次的删除行数；为空时使用默认单批返回。
	nodeDeletes []int64
	// instanceCutoff 记录实例资源采样删除时使用的过期时间边界。
	instanceCutoff time.Time
	// instanceCutoffs 记录实例资源采样每个批次的 cutoff。
	instanceCutoffs []time.Time
	// instanceLimit 记录实例资源采样删除时使用的单批上限。
	instanceLimit int32
	// instanceLimits 记录实例资源采样每个批次的 limit。
	instanceLimits []int32
	// instanceDeletes 允许测试配置实例采样每个批次的删除行数。
	instanceDeletes []int64
}

// DeleteOldNodeResourceSamples 模拟节点采样删除，并保存调用参数供断言使用。
// cutoff 为 time.Time（MySQL DATETIME），与新 ResourceSampleCleanupStore 接口一致。
func (s *resourceSampleCleanupStoreStub) DeleteOldNodeResourceSamples(_ context.Context, cutoff time.Time, limit int32) (int64, error) {
	s.nodeCutoff = cutoff
	s.nodeCutoffs = append(s.nodeCutoffs, cutoff)
	s.nodeLimit = limit
	s.nodeLimits = append(s.nodeLimits, limit)
	if len(s.nodeDeletes) == 0 {
		return 12, nil
	}
	deleted := s.nodeDeletes[0]
	s.nodeDeletes = s.nodeDeletes[1:]
	return deleted, nil
}

// DeleteOldInstanceResourceSamples 模拟实例采样删除，并保存调用参数供断言使用。
func (s *resourceSampleCleanupStoreStub) DeleteOldInstanceResourceSamples(_ context.Context, cutoff time.Time, limit int32) (int64, error) {
	s.instanceCutoff = cutoff
	s.instanceCutoffs = append(s.instanceCutoffs, cutoff)
	s.instanceLimit = limit
	s.instanceLimits = append(s.instanceLimits, limit)
	if len(s.instanceDeletes) == 0 {
		return 34, nil
	}
	deleted := s.instanceDeletes[0]
	s.instanceDeletes = s.instanceDeletes[1:]
	return deleted, nil
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
	// cutoff 是 time.Time（MySQL DATETIME），直接比较不再需要 .Valid 检查。
	assert.Equal(t, expectedCutoff, store.nodeCutoff)
	assert.Equal(t, expectedCutoff, store.instanceCutoff)
	assert.Equal(t, int32(1000), store.nodeLimit)
	assert.Equal(t, int32(1000), store.instanceLimit)
}

// TestResourceSampleCleanupDrainsFullBatches 验证清理任务遇到满批删除时继续追批，避免历史数据积压超过写入速度。
func TestResourceSampleCleanupDrainsFullBatches(t *testing.T) {
	fixedNow := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	expectedCutoff := fixedNow.Add(-30 * 24 * time.Hour)
	store := &resourceSampleCleanupStoreStub{
		nodeDeletes:     []int64{1000, 1000, 25},
		instanceDeletes: []int64{1000, 5},
	}
	cleanup := NewResourceSampleCleanup(store)
	cleanup.SetClock(func() time.Time { return fixedNow })

	nodeDeleted, instanceDeleted, err := cleanup.RunOnce(context.Background())

	require.NoError(t, err)
	assert.Equal(t, int64(2025), nodeDeleted)
	assert.Equal(t, int64(1005), instanceDeleted)
	assert.Len(t, store.nodeCutoffs, 3)
	assert.Len(t, store.instanceCutoffs, 2)
	// cutoff 是 time.Time；每批次应使用相同的保留边界。
	for _, cutoff := range store.nodeCutoffs {
		assert.Equal(t, expectedCutoff, cutoff)
	}
	for _, cutoff := range store.instanceCutoffs {
		assert.Equal(t, expectedCutoff, cutoff)
	}
	assert.Equal(t, []int32{1000, 1000, 1000}, store.nodeLimits)
	assert.Equal(t, []int32{1000, 1000}, store.instanceLimits)
}
