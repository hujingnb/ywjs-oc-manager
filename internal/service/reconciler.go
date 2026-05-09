package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// ReconcilerStore 抽象 reconciler 需要的数据访问能力。
// 与 RuntimeOperationStore 部分重叠，但单独定义避免不相关接口被强行扩展。
type ReconcilerStore interface {
	ListRuntimeNodes(ctx context.Context, arg sqlc.ListRuntimeNodesParams) ([]sqlc.RuntimeNode, error)
	SetRuntimeNodeStatus(ctx context.Context, arg sqlc.SetRuntimeNodeStatusParams) (sqlc.RuntimeNode, error)
	ListAppsByRuntimeNode(ctx context.Context, arg sqlc.ListAppsByRuntimeNodeParams) ([]sqlc.App, error)
	SetAppStatus(ctx context.Context, arg sqlc.SetAppStatusParams) (sqlc.App, error)
}

// NodeHealthReconciler 检测心跳超时的运行节点并把它们标记为 unreachable。
//
// 行为：
//   - 把所有 last_heartbeat_at < now() - threshold 的 active 节点推 unreachable；
//   - 同时把这些节点上 status=running 的应用推 error，让前端立即可见；
//   - 不主动恢复 unreachable → active：恢复必须由 agent 重新发心跳触发。
type NodeHealthReconciler struct {
	store          ReconcilerStore
	now            func() time.Time
	heartbeatGrace time.Duration
}

// NewNodeHealthReconciler 创建节点心跳 reconciler。
// heartbeatGrace 是判定超时的阈值，建议设为 3× 心跳间隔（spec §B5 默认 90s）。
func NewNodeHealthReconciler(store ReconcilerStore, heartbeatGrace time.Duration) *NodeHealthReconciler {
	if heartbeatGrace <= 0 {
		heartbeatGrace = 90 * time.Second
	}
	return &NodeHealthReconciler{store: store, now: time.Now, heartbeatGrace: heartbeatGrace}
}

// SetClock 替换 reconciler 内部时钟，仅供测试使用。
func (r *NodeHealthReconciler) SetClock(now func() time.Time) { r.now = now }

// Reconcile 执行一次扫描。
// 返回检测到的超时节点数；任何错误立即冒泡到调用方（scheduler loop 仅日志输出）。
func (r *NodeHealthReconciler) Reconcile(ctx context.Context) (int, error) {
	nodes, err := r.store.ListRuntimeNodes(ctx, sqlc.ListRuntimeNodesParams{Limit: 500, Offset: 0})
	if err != nil {
		return 0, fmt.Errorf("查询节点失败: %w", err)
	}
	threshold := r.now().Add(-r.heartbeatGrace)
	demoted := 0
	for _, node := range nodes {
		if node.Status != domain.RuntimeNodeStatusActive {
			continue
		}
		if node.LastHeartbeatAt.Valid && node.LastHeartbeatAt.Time.After(threshold) {
			continue
		}
		if _, err := r.store.SetRuntimeNodeStatus(ctx, sqlc.SetRuntimeNodeStatusParams{
			ID:     node.ID,
			Status: domain.RuntimeNodeStatusUnreachable,
		}); err != nil {
			return demoted, fmt.Errorf("更新节点 %s 状态失败: %w", uuidToString(node.ID), err)
		}
		demoted++
		if err := r.markRunningAppsAsError(ctx, node.ID); err != nil {
			return demoted, err
		}
	}
	return demoted, nil
}

// markRunningAppsAsError 把节点上所有 running 应用推到 error 状态。
// 取节点应用列表时分页拉取以避免一次性返回过多记录；这里上限 500，超过则下次循环再处理。
func (r *NodeHealthReconciler) markRunningAppsAsError(ctx context.Context, nodeID pgtype.UUID) error {
	apps, err := r.store.ListAppsByRuntimeNode(ctx, sqlc.ListAppsByRuntimeNodeParams{
		RuntimeNodeID: nodeID,
		Limit:         500,
		Offset:        0,
	})
	if err != nil {
		return fmt.Errorf("列出节点应用失败: %w", err)
	}
	for _, app := range apps {
		if app.Status != domain.AppStatusRunning {
			continue
		}
		if _, err := r.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{
			ID:     app.ID,
			Status: domain.AppStatusError,
		}); err != nil {
			return fmt.Errorf("更新应用 %s 状态失败: %w", uuidToString(app.ID), err)
		}
	}
	return nil
}

// PeriodicReconciler 是一个简单的"周期触发 fn"工具，
// cmd/server 可以用它把多个 reconciler 同时挂在 errgroup 上。
type PeriodicReconciler struct {
	name     string
	interval time.Duration
	fn       func(ctx context.Context) error
}

// NewPeriodicReconciler 创建一个周期任务。
func NewPeriodicReconciler(name string, interval time.Duration, fn func(ctx context.Context) error) *PeriodicReconciler {
	return &PeriodicReconciler{name: name, interval: interval, fn: fn}
}

// Run 在 ctx 取消之前周期触发 fn。任何错误只输出到 logger，不阻断后续轮询。
func (p *PeriodicReconciler) Run(ctx context.Context, logger *slog.Logger) error {
	if p.fn == nil {
		return fmt.Errorf("reconciler %s 未配置 fn", p.name)
	}
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := p.fn(ctx); err != nil {
				logger.ErrorContext(ctx, "reconciler tick 失败",
					"name", p.name,
					"error", err,
				)
			}
		}
	}
}

// Name 返回 reconciler 名称，便于日志输出。
func (p *PeriodicReconciler) Name() string { return p.name }
