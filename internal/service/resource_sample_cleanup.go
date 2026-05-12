package service

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/store/sqlc"
)

const (
	// resourceSampleRetention 是资源趋势数据的保留窗口；超过 30 天的数据按批清理。
	resourceSampleRetention = 30 * 24 * time.Hour
	// resourceSampleBatchSize 控制单次删除行数，避免长事务一次性锁定过多历史采样。
	resourceSampleBatchSize = int32(1000)
)

// ResourceSampleCleanupStore 抽象资源采样清理需要的删除能力。
// service 层只关心 cutoff 和批大小，避免把 sqlc 参数结构体泄漏到清理逻辑里。
type ResourceSampleCleanupStore interface {
	DeleteOldNodeResourceSamples(ctx context.Context, cutoff pgtype.Timestamptz, limit int32) (int64, error)
	DeleteOldInstanceResourceSamples(ctx context.Context, cutoff pgtype.Timestamptz, limit int32) (int64, error)
}

// resourceSampleCleanupSQLCStore 描述 sqlc 生成代码当前暴露的删除方法形状。
// NewResourceSampleCleanup 会把它适配成 ResourceSampleCleanupStore，保持 cmd/server 装配简洁。
type resourceSampleCleanupSQLCStore interface {
	DeleteOldNodeResourceSamples(ctx context.Context, arg sqlc.DeleteOldNodeResourceSamplesParams) (int64, error)
	DeleteOldInstanceResourceSamples(ctx context.Context, arg sqlc.DeleteOldInstanceResourceSamplesParams) (int64, error)
}

// ResourceSampleCleanup 清理超出保留期的节点与实例资源采样。
// now 可在测试中替换，生产环境默认使用 time.Now。
type ResourceSampleCleanup struct {
	store ResourceSampleCleanupStore
	now   func() time.Time
}

// NewResourceSampleCleanup 创建资源采样清理器。
// store 可以直接实现 ResourceSampleCleanupStore，也可以传入 sqlc.Queries 这类生成仓储。
func NewResourceSampleCleanup(store any) *ResourceSampleCleanup {
	return &ResourceSampleCleanup{store: normalizeResourceSampleCleanupStore(store), now: time.Now}
}

// SetClock 替换清理器内部时钟，仅供测试构造固定时间边界。
func (c *ResourceSampleCleanup) SetClock(now func() time.Time) {
	c.now = now
}

// RunOnce 执行一次采样清理。
// 返回值分别是 node_resource_samples 和 instance_resource_samples 删除的行数。
func (c *ResourceSampleCleanup) RunOnce(ctx context.Context) (int64, int64, error) {
	cutoff := pgtype.Timestamptz{Time: c.now().Add(-resourceSampleRetention).UTC(), Valid: true}
	nodeDeleted, err := c.store.DeleteOldNodeResourceSamples(ctx, cutoff, resourceSampleBatchSize)
	if err != nil {
		return nodeDeleted, 0, fmt.Errorf("清理节点资源采样失败: %w", err)
	}
	instanceDeleted, err := c.store.DeleteOldInstanceResourceSamples(ctx, cutoff, resourceSampleBatchSize)
	if err != nil {
		return nodeDeleted, instanceDeleted, fmt.Errorf("清理实例资源采样失败: %w", err)
	}
	return nodeDeleted, instanceDeleted, nil
}

// normalizeResourceSampleCleanupStore 统一测试桩接口和 sqlc 生成接口，
// 让清理逻辑只依赖稳定的 ResourceSampleCleanupStore。
func normalizeResourceSampleCleanupStore(store any) ResourceSampleCleanupStore {
	if direct, ok := store.(ResourceSampleCleanupStore); ok {
		return direct
	}
	if generated, ok := store.(resourceSampleCleanupSQLCStore); ok {
		return resourceSampleCleanupSQLCAdapter{store: generated}
	}
	panic("resource sample cleanup store does not implement required delete methods")
}

// resourceSampleCleanupSQLCAdapter 负责隔离 sqlc 参数结构体，
// 避免保留期和批大小计算逻辑直接耦合到生成代码类型。
type resourceSampleCleanupSQLCAdapter struct {
	store resourceSampleCleanupSQLCStore
}

// DeleteOldNodeResourceSamples 把 service 层的简化参数映射到 sqlc 生成参数。
func (a resourceSampleCleanupSQLCAdapter) DeleteOldNodeResourceSamples(ctx context.Context, cutoff pgtype.Timestamptz, limit int32) (int64, error) {
	return a.store.DeleteOldNodeResourceSamples(ctx, sqlc.DeleteOldNodeResourceSamplesParams{
		CutoffSampledAt: cutoff,
		BatchSize:       limit,
	})
}

// DeleteOldInstanceResourceSamples 把 service 层的简化参数映射到 sqlc 生成参数。
func (a resourceSampleCleanupSQLCAdapter) DeleteOldInstanceResourceSamples(ctx context.Context, cutoff pgtype.Timestamptz, limit int32) (int64, error) {
	return a.store.DeleteOldInstanceResourceSamples(ctx, sqlc.DeleteOldInstanceResourceSamplesParams{
		CutoffSampledAt: cutoff,
		BatchSize:       limit,
	})
}
