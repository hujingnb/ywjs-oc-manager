package handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/runtime"
	"oc-manager/internal/store/sqlc"
)

// RuntimeSnapshotStore 是 RuntimeRefreshStatusHandler 需要的 sqlc 子集。
type RuntimeSnapshotStore interface {
	AppRuntimeStore
	InsertInstanceResourceSample(ctx context.Context, arg sqlc.InsertInstanceResourceSampleParams) (sqlc.InstanceResourceSample, error)
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
	if app.ContainerID.String == "" || !app.RuntimeNodeID.Valid {
		// 容器未就绪时静默成功；scheduler 会在下个 tick 重试。
		return nil
	}
	nodeID := uuidToString(app.RuntimeNodeID)
	sample := sqlc.InsertInstanceResourceSampleParams{
		AppID:         pgtype.UUID{Bytes: app.ID.Bytes, Valid: true},
		RuntimeNodeID: pgtype.UUID{Bytes: app.RuntimeNodeID.Bytes, Valid: true},
		ContainerID:   app.ContainerID.String,
		SampledAt:     pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
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
		sample.CpuPercent = pgtype.Float8{Float64: stats.CPUPercent, Valid: true}
		sample.MemoryUsedBytes = uint64ToInt8(stats.MemoryUsage)
		sample.MemoryLimitBytes = uint64ToInt8(stats.MemoryLimit)
		sample.DiskReadBytes = uint64ToInt8(stats.DiskReadBytes)
		sample.DiskWriteBytes = uint64ToInt8(stats.DiskWriteBytes)
		sample.NetworkRxBytes = uint64ToInt8(stats.NetworkRxBytes)
		sample.NetworkTxBytes = uint64ToInt8(stats.NetworkTxBytes)
	}
	if _, err := h.store.InsertInstanceResourceSample(ctx, sample); err != nil {
		return fmt.Errorf("写入实例资源采样失败: %w", err)
	}
	return nil
}

// textOrNull 将错误与状态字段写成 nullable text，避免空字符串污染采样表。
func textOrNull(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}

// uint64ToInt8 将 Docker 累计字节数写入 pg int8；项目运行指标预期不会超过 int64 上限。
func uint64ToInt8(value uint64) pgtype.Int8 {
	return pgtype.Int8{Int64: int64(value), Valid: true}
}
