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
	// ClaimAICCRolloutAppOwnership 原子领取跨类型 app guard；旧 owner 非活跃时允许安全接管。
	ClaimAICCRolloutAppOwnership(ctx context.Context, arg sqlc.ClaimAICCRolloutAppOwnershipParams) error
	// GetAICCRolloutAppOwnership 返回 claim 后的实际 owner，异类活跃 owner 时任务必须 defer。
	GetAICCRolloutAppOwnership(ctx context.Context, appID string) (sqlc.AiccRolloutAppOwner, error)
	// SetAppRuntimePhaseReadyForAICCRolloutOwner 仅允许本任务 guard 仍存在时收口 ready。
	SetAppRuntimePhaseReadyForAICCRolloutOwner(ctx context.Context, arg sqlc.SetAppRuntimePhaseReadyForAICCRolloutOwnerParams) (int64, error)
	// ReleaseAICCRolloutAppOwnership 在 marker 清除后释放本任务 own guard。
	ReleaseAICCRolloutAppOwnership(ctx context.Context, arg sqlc.ReleaseAICCRolloutAppOwnershipParams) (int64, error)
	ReleaseAICCRolloutAppOwnershipByOwner(ctx context.Context, arg sqlc.ReleaseAICCRolloutAppOwnershipByOwnerParams) (int64, error)
}

// AICCPlatformPromptRolloutSuccessorEnqueuer 在旧 hash 下发完成后，检查是否需为当前 hash 创建后继任务。
type AICCPlatformPromptRolloutSuccessorEnqueuer interface {
	EnqueueIfNeeded(ctx context.Context) error
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
	store     AICCPlatformPromptRolloutStore
	orch      k8sorch.Orchestrator
	timeout   time.Duration
	successor AICCPlatformPromptRolloutSuccessorEnqueuer
}

// SetSuccessorEnqueuer 注入 singleton 协调器；服务启动时设置，避免 worker 直接复制创建去重逻辑。
func (h *AICCPlatformPromptRolloutHandler) SetSuccessorEnqueuer(enqueuer AICCPlatformPromptRolloutSuccessorEnqueuer) {
	h.successor = enqueuer
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
		// 上次完成 clear 后若 release 因瞬态数据库错误失败，重试先清理自己的孤儿 guard；
		// 不会释放其他任务 ownership，也不会遗漏仍有 marker 的恢复路径。
		if payload.RepairAgentID == "" {
			if err := h.releaseOwnership(ctx, job.ID, ""); err != nil {
				return err
			}
		}
		leader, err := h.store.GetAICCPlatformPromptRolloutLeaderJob(ctx)
		if err != nil {
			return aiccPlatformPromptRolloutStageError("-", "elect_leader", err)
		}
		if leader.ID != job.ID {
			return &DeferredJobError{Delay: aiccPlatformPromptRolloutDeferDelay, Reason: fmt.Sprintf("等待平台提示词 leader job=%s", leader.ID)}
		}
		if payload.RepairAgentID != "" {
			if err := h.claimOwnership(ctx, job.ID, payload.RepairAppID); err != nil {
				return err
			}
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
			if h.successor != nil {
				if err := h.successor.EnqueueIfNeeded(ctx); err != nil {
					return aiccPlatformPromptRolloutStageError("-", "enqueue_successor", err)
				}
			}
			return nil
		}
		agent := agents[0]
		if err := h.claimOwnership(ctx, job.ID, agent.AppID); err != nil {
			return err
		}
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
	readyRows, err := h.store.SetAppRuntimePhaseReadyForAICCRolloutOwner(ctx, sqlc.SetAppRuntimePhaseReadyForAICCRolloutOwnerParams{
		ID: payload.RepairAppID, OwnerJobID: jobID, OwnerJobType: domain.JobTypeAICCPlatformPromptRollout,
	})
	if err != nil {
		return aiccPlatformPromptRolloutStageError(payload.RepairAgentID, "mark_ready", err)
	}
	if readyRows != 1 {
		return aiccPlatformPromptRolloutStageError(payload.RepairAgentID, "mark_ready", fmt.Errorf("ownership 不再属于当前任务，影响行数=%d", readyRows))
	}
	agentID := payload.RepairAgentID
	appID := payload.RepairAppID
	payload.RepairAgentID = ""
	payload.RepairAppID = ""
	payload.RepairTargetGeneration = 0
	if err := h.persistPayload(ctx, jobID, *payload); err != nil {
		return aiccPlatformPromptRolloutStageError(agentID, "clear_repair_marker", err)
	}
	return h.releaseOwnership(ctx, jobID, appID)
}

// claimOwnership 在持久 marker 或外部 restart 前领取跨类型 guard；异类活跃 owner 使任务 defer。
func (h *AICCPlatformPromptRolloutHandler) claimOwnership(ctx context.Context, jobID, appID string) error {
	if err := h.store.ClaimAICCRolloutAppOwnership(ctx, sqlc.ClaimAICCRolloutAppOwnershipParams{AppID: appID, OwnerJobID: jobID, OwnerJobType: domain.JobTypeAICCPlatformPromptRollout}); err != nil {
		return aiccPlatformPromptRolloutStageError("-", "claim_ownership", err)
	}
	owner, err := h.store.GetAICCRolloutAppOwnership(ctx, appID)
	if err != nil {
		return aiccPlatformPromptRolloutStageError("-", "read_ownership", err)
	}
	if owner.OwnerJobID != jobID || owner.OwnerJobType != domain.JobTypeAICCPlatformPromptRollout {
		return &DeferredJobError{Delay: aiccPlatformPromptRolloutDeferDelay, Reason: fmt.Sprintf("等待 app=%s owner job=%s type=%s", appID, owner.OwnerJobID, owner.OwnerJobType)}
	}
	return nil
}

// releaseOwnership 只释放当前任务自己的 guard；appID 为空表示无 marker 重试时批量前的单 app 未知清理由 store 忽略。
func (h *AICCPlatformPromptRolloutHandler) releaseOwnership(ctx context.Context, jobID, appID string) error {
	var rows int64
	var err error
	if appID == "" {
		rows, err = h.store.ReleaseAICCRolloutAppOwnershipByOwner(ctx, sqlc.ReleaseAICCRolloutAppOwnershipByOwnerParams{OwnerJobID: jobID, OwnerJobType: domain.JobTypeAICCPlatformPromptRollout})
	} else {
		rows, err = h.store.ReleaseAICCRolloutAppOwnership(ctx, sqlc.ReleaseAICCRolloutAppOwnershipParams{AppID: appID, OwnerJobID: jobID, OwnerJobType: domain.JobTypeAICCPlatformPromptRollout})
	}
	if err != nil {
		return aiccPlatformPromptRolloutStageError("-", "release_ownership", err)
	}
	if rows > 1 {
		return aiccPlatformPromptRolloutStageError("-", "release_ownership", fmt.Errorf("释放 ownership 影响行数异常: %d", rows))
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
