package handlers

import (
	"context"
	"encoding/json"
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
	SetAppRuntimeSnapshot(ctx context.Context, arg sqlc.SetAppRuntimeSnapshotParams) (sqlc.App, error)
}

// RuntimeInspector 抽象 worker 拉取容器实时状态 + 指标的能力，便于 fake。
type RuntimeInspector interface {
	InspectContainer(ctx context.Context, nodeID, containerID string) (runtime.ContainerInfo, error)
	ContainerStats(ctx context.Context, nodeID, containerID string) (runtime.ContainerStats, error)
}

// RuntimeRefreshStatusHandler 周期性写 apps.runtime_snapshot_json：
// scheduler 每 30s 给每个 running 应用入队一条；handler 调 docker inspect + stats 后写库，
// 前端 AppRuntimeTab 读这一列展示资源占用。
//
// 任一步骤失败仅记录到 last_error 不会改 app.status；status 翻转交给 Task 11 的
// app_health_check handler，避免单次 stats 抖动让 UI 闪烁。
type RuntimeRefreshStatusHandler struct {
	store     RuntimeSnapshotStore
	inspector RuntimeInspector
}

// AppRuntimeSnapshot 是写入 apps.runtime_snapshot_json 的归一化结构。
// 字段保持平铺，前端不需要再展开 nested map，且方便后续 timeseries 升级。
type AppRuntimeSnapshot struct {
	ContainerID    string    `json:"container_id"`
	ContainerName  string    `json:"container_name"`
	ContainerImage string    `json:"container_image,omitempty"`
	Status         string    `json:"status"`
	CPUPercent     float64   `json:"cpu_percent"`
	MemoryUsage    uint64    `json:"memory_usage_bytes"`
	MemoryLimit    uint64    `json:"memory_limit_bytes"`
	NetworkRxBytes uint64    `json:"network_rx_bytes"`
	NetworkTxBytes uint64    `json:"network_tx_bytes"`
	CollectedAt    time.Time `json:"collected_at"`
	// LastError 在 inspect/stats 任一失败时填入，CollectedAt 仍写入便于 UI 展示「最近尝试」时间。
	LastError string `json:"last_error,omitempty"`
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
	snapshot := AppRuntimeSnapshot{
		ContainerID: app.ContainerID.String,
		CollectedAt: time.Now().UTC(),
	}
	if info, err := h.inspector.InspectContainer(ctx, nodeID, app.ContainerID.String); err != nil {
		snapshot.LastError = fmt.Sprintf("inspect: %v", err)
	} else {
		snapshot.ContainerName = info.Name
		snapshot.ContainerImage = info.Image
		snapshot.Status = info.Status
	}
	if stats, err := h.inspector.ContainerStats(ctx, nodeID, app.ContainerID.String); err != nil {
		// 只在 inspect 没记错时覆盖；保留首错以便排障。
		if snapshot.LastError == "" {
			snapshot.LastError = fmt.Sprintf("stats: %v", err)
		}
	} else {
		snapshot.CPUPercent = stats.CPUPercent
		snapshot.MemoryUsage = stats.MemoryUsage
		snapshot.MemoryLimit = stats.MemoryLimit
		snapshot.NetworkRxBytes = stats.NetworkRxBytes
		snapshot.NetworkTxBytes = stats.NetworkTxBytes
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("序列化 runtime snapshot 失败: %w", err)
	}
	if _, err := h.store.SetAppRuntimeSnapshot(ctx, sqlc.SetAppRuntimeSnapshotParams{
		ID:                  pgtype.UUID{Bytes: app.ID.Bytes, Valid: true},
		RuntimeSnapshotJson: encoded,
	}); err != nil {
		return fmt.Errorf("写入 runtime snapshot 失败: %w", err)
	}
	return nil
}
