// Package handlers 中的本文件实现 AICC 平台提示词 hash 的独立逐台静默下发任务。
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/k8sorch"
	"oc-manager/internal/store/sqlc"
)

// AICCPlatformPromptRolloutStore 是平台提示词下发所需的最小持久化接口。
// 它不暴露企业 AICC 配置或 revision 写入，避免与模型 rollout 发生职责混淆。
type AICCPlatformPromptRolloutStore interface {
	// GetAICCPlatformPromptRolloutLeaderJob 获取全局同类 pending/running 任务的稳定 leader。
	GetAICCPlatformPromptRolloutLeaderJob(ctx context.Context) (sqlc.Job, error)
	// ListPendingAICCPlatformPromptRolloutAgents 按客服 ID 稳定顺序领取一台 hash 落后 active 客服。
	ListPendingAICCPlatformPromptRolloutAgents(ctx context.Context, arg sqlc.ListPendingAICCPlatformPromptRolloutAgentsParams) ([]sqlc.AiccAgent, error)
	// GetAppAppliedPlatformPromptHash 读取 bootstrap 在目标 Deployment 中实际写回的 hash。
	GetAppAppliedPlatformPromptHash(ctx context.Context, appID string) (string, error)
	// SetAppRuntimePhase 显示每台 Deployment 处于 restarting 或 ready 的运行时窗口。
	SetAppRuntimePhase(ctx context.Context, arg sqlc.SetAppRuntimePhaseParams) error
	// UpdateJobPayload 持久化任务的单台恢复 marker，进程失败后可从 generation 继续。
	UpdateJobPayload(ctx context.Context, arg sqlc.UpdateJobPayloadParams) (int64, error)
}

// AICCPlatformPromptRolloutPayload 是全局提示词任务的最小载荷。
type AICCPlatformPromptRolloutPayload struct {
	// TargetPromptHash 是本轮必须由 bootstrap 写入 app 的平台提示词 hash。
	TargetPromptHash string `json:"target_prompt_hash"`
	// RepairAgentID 表示任务已持有但尚未完整收口的客服。
	RepairAgentID string `json:"repair_agent_id,omitempty"`
	// RepairAppID 是重启、等待和校验 hash 所针对的 app。
	RepairAppID string `json:"repair_app_id,omitempty"`
	// RepairTargetGeneration 为零表示 marker 已持久化但 restart 尚未成功返回 generation。
	RepairTargetGeneration int64 `json:"repair_target_generation"`
}

// AICCPlatformPromptRolloutHandler 逐台滚动重启提示词 hash 落后的 AICC Deployment。
type AICCPlatformPromptRolloutHandler struct {
	store   AICCPlatformPromptRolloutStore
	orch    k8sorch.Orchestrator
	timeout time.Duration
}

// NewAICCPlatformPromptRolloutHandler 构造独立 handler；依赖缺失会在执行时返回可重试诊断错误。
func NewAICCPlatformPromptRolloutHandler(store AICCPlatformPromptRolloutStore, orch k8sorch.Orchestrator, timeout time.Duration) *AICCPlatformPromptRolloutHandler {
	return &AICCPlatformPromptRolloutHandler{store: store, orch: orch, timeout: timeout}
}

var _ HandlerFunc = (*AICCPlatformPromptRolloutHandler)(nil).Handle

// aiccPlatformPromptRolloutDeferDelay 让非 leader 任务短暂回队列，不占用 worker 并保持全局逐台语义。
const aiccPlatformPromptRolloutDeferDelay = 2 * time.Second

// Handle 只处理 payload hash 落后的 active 客服；每台完全收口后才继续下一台。
func (h *AICCPlatformPromptRolloutHandler) Handle(ctx context.Context, job sqlc.Job) error {
	var payload AICCPlatformPromptRolloutPayload
	if err := json.Unmarshal(job.PayloadJson, &payload); err != nil {
		return fmt.Errorf("解析 aicc_platform_prompt_rollout payload 失败: %w", err)
	}
	if payload.TargetPromptHash == "" {
		return errors.New("aicc_platform_prompt_rollout payload 缺少 target_prompt_hash")
	}
	if h.store == nil {
		return aiccPlatformPromptRolloutStageError("-", "validate_dependencies", errors.New("未配置持久化 store"))
	}
	if h.orch == nil {
		return aiccPlatformPromptRolloutStageError("-", "validate_dependencies", errors.New("Kubernetes 编排器未启用"))
	}
	if h.timeout <= 0 {
		return aiccPlatformPromptRolloutStageError("-", "validate_timeout", errors.New("等待超时必须大于 0"))
	}

	for {
		leader, err := h.store.GetAICCPlatformPromptRolloutLeaderJob(ctx)
		if err != nil {
			return aiccPlatformPromptRolloutStageError("-", "elect_leader", err)
		}
		if leader.ID != job.ID {
			return &DeferredJobError{Delay: aiccPlatformPromptRolloutDeferDelay, Reason: fmt.Sprintf("等待平台提示词 leader job=%s", leader.ID)}
		}
		if payload.RepairAgentID != "" {
			if err := h.recoverMarkedAgent(ctx, job.ID, &payload); err != nil {
				return err
			}
			continue
		}

		agents, err := h.store.ListPendingAICCPlatformPromptRolloutAgents(ctx, sqlc.ListPendingAICCPlatformPromptRolloutAgentsParams{
			AppliedPlatformPromptHash: payload.TargetPromptHash,
			Limit:                     1,
		})
		if err != nil {
			return aiccPlatformPromptRolloutStageError("-", "list_pending", err)
		}
		if len(agents) == 0 {
			return nil
		}
		agent := agents[0]
		payload.RepairAgentID = agent.ID
		payload.RepairAppID = agent.AppID
		payload.RepairTargetGeneration = 0
		if err := h.persistPayload(ctx, job.ID, payload); err != nil {
			return aiccPlatformPromptRolloutStageError(agent.ID, "persist_repair_marker", err)
		}
		if err := h.recoverMarkedAgent(ctx, job.ID, &payload); err != nil {
			return err
		}
	}
}

// recoverMarkedAgent 从 marker 恢复一台客服；generation=0 时才允许触发一次新的 rollout restart。
func (h *AICCPlatformPromptRolloutHandler) recoverMarkedAgent(ctx context.Context, jobID string, payload *AICCPlatformPromptRolloutPayload) error {
	if payload.RepairAppID == "" || payload.RepairTargetGeneration < 0 {
		return aiccPlatformPromptRolloutStageError(payload.RepairAgentID, "validate_repair_marker", errors.New("任务恢复标记不完整"))
	}
	if payload.RepairTargetGeneration == 0 {
		if err := h.store.SetAppRuntimePhase(ctx, sqlc.SetAppRuntimePhaseParams{ID: payload.RepairAppID, RuntimePhase: domain.RuntimePhaseRestarting}); err != nil {
			return aiccPlatformPromptRolloutStageError(payload.RepairAgentID, "mark_restarting", err)
		}
		generation, err := h.orch.RolloutRestartAndGetGeneration(ctx, payload.RepairAppID)
		if err != nil {
			return aiccPlatformPromptRolloutStageError(payload.RepairAgentID, "restart", err)
		}
		if generation <= 0 {
			return aiccPlatformPromptRolloutStageError(payload.RepairAgentID, "restart", fmt.Errorf("返回无效 generation %d", generation))
		}
		payload.RepairTargetGeneration = generation
		if err := h.persistPayload(ctx, jobID, *payload); err != nil {
			return aiccPlatformPromptRolloutStageError(payload.RepairAgentID, "persist_repair_generation", err)
		}
	}
	if err := h.orch.WaitRolloutReady(ctx, payload.RepairAppID, payload.RepairTargetGeneration, h.timeout, nil); err != nil {
		return aiccPlatformPromptRolloutStageError(payload.RepairAgentID, "wait_rollout_ready", err)
	}
	appliedHash, err := h.store.GetAppAppliedPlatformPromptHash(ctx, payload.RepairAppID)
	if err != nil {
		return aiccPlatformPromptRolloutStageError(payload.RepairAgentID, "read_applied_prompt_hash", err)
	}
	if appliedHash != payload.TargetPromptHash {
		return aiccPlatformPromptRolloutStageError(payload.RepairAgentID, "verify_prompt_hash", fmt.Errorf("bootstrap 已应用 hash=%q，目标 hash=%q", appliedHash, payload.TargetPromptHash))
	}
	if err := h.store.SetAppRuntimePhase(ctx, sqlc.SetAppRuntimePhaseParams{ID: payload.RepairAppID, RuntimePhase: domain.RuntimePhaseReady}); err != nil {
		return aiccPlatformPromptRolloutStageError(payload.RepairAgentID, "mark_ready", err)
	}
	agentID := payload.RepairAgentID
	payload.RepairAgentID = ""
	payload.RepairAppID = ""
	payload.RepairTargetGeneration = 0
	if err := h.persistPayload(ctx, jobID, *payload); err != nil {
		return aiccPlatformPromptRolloutStageError(agentID, "clear_repair_marker", err)
	}
	return nil
}

// persistPayload 仅接受当前 running job 的单行更新，避免失去 marker 时误把另一任务当作本任务恢复。
func (h *AICCPlatformPromptRolloutHandler) persistPayload(ctx context.Context, jobID string, payload AICCPlatformPromptRolloutPayload) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	rows, err := h.store.UpdateJobPayload(ctx, sqlc.UpdateJobPayloadParams{ID: jobID, PayloadJson: raw})
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("更新 running job payload 影响行数异常: %d", rows)
	}
	return nil
}

// aiccPlatformPromptRolloutStageError 统一标注客服与失败阶段，便于 worker last_error 直接定位。
func aiccPlatformPromptRolloutStageError(agentID, stage string, cause error) error {
	return fmt.Errorf("AICC 平台提示词 rollout 失败 agent=%s stage=%s: %w", agentID, stage, cause)
}
