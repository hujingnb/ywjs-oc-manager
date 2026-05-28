package service

import (
	"context"
	"fmt"

	"github.com/guregu/null/v5"

	"oc-manager/internal/domain"
	agentint "oc-manager/internal/integrations/agent"
	"oc-manager/internal/store/sqlc"
)

// RuntimeNodeProbeStore 抽象主动探测需要的节点查询和状态更新能力。
type RuntimeNodeProbeStore interface {
	ListRuntimeNodes(ctx context.Context, arg sqlc.ListRuntimeNodesParams) ([]sqlc.RuntimeNode, error)
	UpdateRuntimeNodeProbeSuccess(ctx context.Context, arg sqlc.UpdateRuntimeNodeProbeSuccessParams) error
	UpdateRuntimeNodeProbeFailure(ctx context.Context, arg sqlc.UpdateRuntimeNodeProbeFailureParams) error
	GetRuntimeNode(ctx context.Context, id string) (sqlc.RuntimeNode, error)
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) error
}

// RuntimeNodeTokenResolver 抽象按 nodeID 读取 agent token 的能力。
type RuntimeNodeTokenResolver interface {
	Get(nodeID string) (string, error)
}

// RuntimeNodeProbeClient 抽象实际网络探测，便于单元测试注入。
type RuntimeNodeProbeClient interface {
	Probe(ctx context.Context, dockerEndpoint, fileEndpoint, token, caCertPEM string) agentint.ProbeResult
}

// RuntimeNodeProbeConfig 控制探测阈值。
type RuntimeNodeProbeConfig struct {
	FailureThreshold  int32
	RecoveryThreshold int32
}

// RuntimeNodeProbeReconciler 周期性探测 active/degraded 节点的 agent 入站端口。
type RuntimeNodeProbeReconciler struct {
	store  RuntimeNodeProbeStore
	tokens RuntimeNodeTokenResolver
	client RuntimeNodeProbeClient
	cfg    RuntimeNodeProbeConfig
}

// NewRuntimeNodeProbeReconciler 创建 runtime node 主动探测 reconciler。
func NewRuntimeNodeProbeReconciler(store RuntimeNodeProbeStore, tokens RuntimeNodeTokenResolver, client RuntimeNodeProbeClient, cfg RuntimeNodeProbeConfig) *RuntimeNodeProbeReconciler {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 3
	}
	if cfg.RecoveryThreshold <= 0 {
		cfg.RecoveryThreshold = 2
	}
	return &RuntimeNodeProbeReconciler{store: store, tokens: tokens, client: client, cfg: cfg}
}

// Reconcile 执行一次探测扫描，返回被探测节点数量。
func (r *RuntimeNodeProbeReconciler) Reconcile(ctx context.Context) (int, error) {
	nodes, err := r.store.ListRuntimeNodes(ctx, sqlc.ListRuntimeNodesParams{Limit: 500, Offset: 0})
	if err != nil {
		return 0, fmt.Errorf("查询节点失败: %w", err)
	}
	checked := 0
	for _, node := range nodes {
		if node.Status != domain.RuntimeNodeStatusActive && node.Status != domain.RuntimeNodeStatusDegraded {
			continue
		}
		checked++
		if err := r.probeNode(ctx, node); err != nil {
			return checked, err
		}
	}
	return checked, nil
}

func (r *RuntimeNodeProbeReconciler) probeNode(ctx context.Context, node sqlc.RuntimeNode) error {
	// node.ID 已是 string；直接使用。
	nodeID := node.ID
	token, err := r.tokens.Get(nodeID)
	if err != nil {
		return r.recordFailure(ctx, node, "agent_token_missing: "+err.Error())
	}
	result := r.client.Probe(ctx,
		node.AgentDockerEndpoint.String,
		node.AgentFileEndpoint.String,
		token,
		node.AgentTlsCaCert.String,
	)
	if result.OK {
		return r.recordSuccess(ctx, node)
	}
	return r.recordFailure(ctx, node, result.Error)
}

func (r *RuntimeNodeProbeReconciler) recordSuccess(ctx context.Context, node sqlc.RuntimeNode) error {
	before := node.Status
	// UpdateRuntimeNodeProbeSuccess 为 :exec；写入后通过 GetRuntimeNode 读回以获取最新 status。
	if err := r.store.UpdateRuntimeNodeProbeSuccess(ctx, sqlc.UpdateRuntimeNodeProbeSuccessParams{
		ID:                 node.ID,
		ProbeSuccessStreak: r.cfg.RecoveryThreshold,
	}); err != nil {
		return fmt.Errorf("更新节点 %s probe 成功状态失败: %w", node.ID, err)
	}
	updated, err := r.store.GetRuntimeNode(ctx, node.ID)
	if err != nil {
		return fmt.Errorf("读取 probe 成功后节点 %s 失败: %w", node.ID, err)
	}
	if before == domain.RuntimeNodeStatusDegraded && updated.Status == domain.RuntimeNodeStatusActive {
		return r.audit(ctx, updated.ID, "node_probe_recovered", before, updated.Status)
	}
	return nil
}

func (r *RuntimeNodeProbeReconciler) recordFailure(ctx context.Context, node sqlc.RuntimeNode, message string) error {
	before := node.Status
	// UpdateRuntimeNodeProbeFailure 为 :exec；LastProbeError 是 null.String。
	if err := r.store.UpdateRuntimeNodeProbeFailure(ctx, sqlc.UpdateRuntimeNodeProbeFailureParams{
		ID:                 node.ID,
		ProbeFailureStreak: r.cfg.FailureThreshold,
		LastProbeError:     null.StringFrom(message),
	}); err != nil {
		return fmt.Errorf("更新节点 %s probe 失败状态失败: %w", node.ID, err)
	}
	updated, err := r.store.GetRuntimeNode(ctx, node.ID)
	if err != nil {
		return fmt.Errorf("读取 probe 失败后节点 %s 失败: %w", node.ID, err)
	}
	if before == domain.RuntimeNodeStatusActive && updated.Status == domain.RuntimeNodeStatusDegraded {
		return r.audit(ctx, updated.ID, "node_probe_degraded", before, updated.Status)
	}
	return nil
}

// audit 写一条节点 probe 审计。before / after 是切换前后的节点状态，
// 拼成详情字符串「状态：X → Y」，便于审计列表识别状态变化方向。
// nodeID 已是 string，直接使用。
func (r *RuntimeNodeProbeReconciler) audit(ctx context.Context, nodeID string, action, before, after string) error {
	if err := r.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
		ID:            newUUID(),
		ActorRole:     "system",
		TargetType:    "runtime_node",
		TargetID:      nodeID,
		Action:        action,
		Result:        "succeeded",
		DetailMessage: null.StringFrom(fmt.Sprintf("状态：%s → %s", before, after)),
	}); err != nil {
		return fmt.Errorf("写节点 probe 审计失败: %w", err)
	}
	return nil
}
