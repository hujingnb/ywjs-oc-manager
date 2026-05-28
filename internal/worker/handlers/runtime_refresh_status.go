package handlers

import (
	"context"
	"fmt"
	"time"

	null "github.com/guregu/null/v5"
	"github.com/google/uuid"

	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/runtime"
	"oc-manager/internal/store/sqlc"
)

// RuntimeSnapshotStore 是 RuntimeRefreshStatusHandler 需要的 sqlc 子集。
type RuntimeSnapshotStore interface {
	AppRuntimeStore
	InsertInstanceResourceSample(ctx context.Context, arg sqlc.InsertInstanceResourceSampleParams) error
}

// RuntimeInspector 抽象 worker 拉取容器实时状态 + 指标的能力，便于 fake。
type RuntimeInspector interface {
	InspectContainer(ctx context.Context, nodeID, containerID string) (runtime.ContainerInfo, error)
	ContainerStats(ctx context.Context, nodeID, containerID string) (runtime.ContainerStats, error)
}

// RuntimeRefreshStatusHandler 周期性写 instance_resource_samples：
// scheduler 每 30s 给每个 running 应用入队一条；handler 调 docker inspect + stats 后写采样表，
// 前端资源展示读取最新实例采样，不再依赖 apps.runtime_snapshot_json。
//
// 任一步骤失败仅记录到 last_error 不会改 app.status；status 翻转交给 Task 11 的
// app_health_check handler，避免单次 stats 抖动让 UI 闪烁。
type RuntimeRefreshStatusHandler struct {
	store     RuntimeSnapshotStore
	inspector RuntimeInspector
}

// NewRuntimeRefreshStatusHandler 创建 handler。
func NewRuntimeRefreshStatusHandler(store RuntimeSnapshotStore, inspector RuntimeInspector) *RuntimeRefreshStatusHandler {
	return &RuntimeRefreshStatusHandler{store: store, inspector: inspector}
}

// Handle 执行 runtime_refresh_status job。
func (h *RuntimeRefreshStatusHandler) Handle(ctx context.Context, job sqlc.Job) error {
	if job.Type != domain.JobTypeRuntimeRefreshStatus {
		return fmt.Errorf("非 runtime_refresh_status 任务: %s", job.Type)
	}
	payload, err := decodeAppOpPayload(job.PayloadJson)
	if err != nil {
		return err
	}
	app, _, err := loadApp(ctx, h.store, payload)
	if err != nil {
		return err
	}
	// RuntimeNodeID 是 string，空值表示未分配节点；无节点或无容器时静默成功。
	if app.ContainerID.String == "" || app.RuntimeNodeID == "" {
		// 容器未就绪时静默成功；scheduler 会在下个周期重试。
		return nil
	}
	nodeID := app.RuntimeNodeID
	sample := sqlc.InsertInstanceResourceSampleParams{
		ID:            uuid.NewString(),
		AppID:         app.ID,
		RuntimeNodeID: app.RuntimeNodeID,
		ContainerID:   app.ContainerID.String,
		SampledAt:     time.Now().UTC(),
	}
	if info, err := h.inspector.InspectContainer(ctx, nodeID, app.ContainerID.String); err != nil {
		sample.LastError = textOrNull(fmt.Sprintf("inspect: %v", err))
	} else {
		sample.ContainerStatus = textOrNull(info.Status)
	}
	if stats, err := h.inspector.ContainerStats(ctx, nodeID, app.ContainerID.String); err != nil {
		// 只在 inspect 没记错时覆盖；保留首错以便排障。
		if !sample.LastError.Valid {
			sample.LastError = textOrNull(fmt.Sprintf("stats: %v", err))
		}
	} else {
		sample.CpuPercent = null.FloatFrom(stats.CPUPercent)
		sample.MemoryUsedBytes = null.IntFrom(int64(stats.MemoryUsage))
		sample.MemoryLimitBytes = null.IntFrom(int64(stats.MemoryLimit))
		sample.DiskReadBytes = null.IntFrom(int64(stats.DiskReadBytes))
		sample.DiskWriteBytes = null.IntFrom(int64(stats.DiskWriteBytes))
		sample.NetworkRxBytes = null.IntFrom(int64(stats.NetworkRxBytes))
		sample.NetworkTxBytes = null.IntFrom(int64(stats.NetworkTxBytes))
	}
	if err := h.store.InsertInstanceResourceSample(ctx, sample); err != nil {
		return fmt.Errorf("写入实例资源采样失败: %w", err)
	}
	return nil
}

// textOrNull 将错误与状态字段写成 nullable text，避免空字符串污染采样表。
func textOrNull(value string) null.String {
	return null.NewString(value, value != "")
}
