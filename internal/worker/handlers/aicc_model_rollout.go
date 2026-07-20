// Package handlers 中的本文件实现企业 AICC 模型配置逐台静默切换任务。
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

// AICCModelRolloutStore 是逐台切换所需的最小持久化接口。
type AICCModelRolloutStore interface {
	// GetAICCModelRolloutLeaderJob 从数据库选出同企业唯一活跃 leader，跨进程约束副作用。
	GetAICCModelRolloutLeaderJob(ctx context.Context, orgID json.RawMessage) (sqlc.Job, error)
	// GetOrganizationAICCConfig 每轮读取最新 revision，以便旧任务合并连续配置变更。
	GetOrganizationAICCConfig(ctx context.Context, orgID string) (sqlc.OrganizationAiccConfig, error)
	// ListPendingAICCModelRolloutAgents 按稳定顺序仅领取一台尚未应用目标 revision 的 active 智能体。
	ListPendingAICCModelRolloutAgents(ctx context.Context, arg sqlc.ListPendingAICCModelRolloutAgentsParams) ([]sqlc.AiccAgent, error)
	// SetAICCAgentAppliedConfigRevision 仅在新 Deployment 完整就绪后单调写入应用进度。
	SetAICCAgentAppliedConfigRevision(ctx context.Context, arg sqlc.SetAICCAgentAppliedConfigRevisionParams) error
	// SetAppRuntimePhase 向运行时状态轴暴露逐台重启窗口。
	SetAppRuntimePhase(ctx context.Context, arg sqlc.SetAppRuntimePhaseParams) error
	// UpdateJobPayload 持久化任务专属恢复标记，跨进程记录已核验的 generation 与目标智能体。
	UpdateJobPayload(ctx context.Context, arg sqlc.UpdateJobPayloadParams) (int64, error)
	ClaimAICCRolloutAppOwnership(ctx context.Context, arg sqlc.ClaimAICCRolloutAppOwnershipParams) error
	GetAICCRolloutAppOwnership(ctx context.Context, appID string) (sqlc.AiccRolloutAppOwner, error)
	SetAppRuntimePhaseReadyForAICCRolloutOwner(ctx context.Context, arg sqlc.SetAppRuntimePhaseReadyForAICCRolloutOwnerParams) (int64, error)
	ReleaseAICCRolloutAppOwnership(ctx context.Context, arg sqlc.ReleaseAICCRolloutAppOwnershipParams) (int64, error)
	ReleaseAICCRolloutAppOwnershipByOwner(ctx context.Context, arg sqlc.ReleaseAICCRolloutAppOwnershipByOwnerParams) (int64, error)
}

// AICCModelRolloutPayload 是配置更新事务写入持久 job 的最小载荷。
type AICCModelRolloutPayload struct {
	// OrgID 限定本次 rollout 只能处理单个企业。
	OrgID string `json:"org_id"`
	// TargetRevision 是任务创建时 revision；执行时会与数据库最新 revision 合并。
	TargetRevision int32 `json:"target_revision"`
	// RepairAgentID 标识已被本任务持有、但 restart/wait/stamp/ready/clear 尚未全部完成的智能体。
	RepairAgentID string `json:"repair_agent_id,omitempty"`
	// RepairAppID 是恢复阶段重新等待 Deployment 和写 runtime phase 的应用 ID。
	RepairAppID string `json:"repair_app_id,omitempty"`
	// RepairTargetGeneration 为 0 表示 ownership 已持久但 restart 尚未返回；正数绝对绑定本次 Deployment generation。
	RepairTargetGeneration int64 `json:"repair_target_generation"`
	// RepairTargetRevision 是本轮允许写入的配置 revision，恢复时禁止改用更高 revision。
	RepairTargetRevision int32 `json:"repair_target_revision,omitempty"`
}

// AICCModelRolloutHandler 逐台重启 AICC Deployment，并以 generation 就绪事实确认应用进度。
type AICCModelRolloutHandler struct {
	store   AICCModelRolloutStore
	orch    k8sorch.Orchestrator
	timeout time.Duration
}

// NewAICCModelRolloutHandler 构造 rollout handler；orch 缺失会在执行时返回可诊断错误供 job 重试。
func NewAICCModelRolloutHandler(store AICCModelRolloutStore, orch k8sorch.Orchestrator, timeout time.Duration) *AICCModelRolloutHandler {
	return &AICCModelRolloutHandler{store: store, orch: orch, timeout: timeout}
}

var _ HandlerFunc = (*AICCModelRolloutHandler)(nil).Handle

// aiccModelRolloutDeferDelay 是 follower 无损回队列的短延迟，避免紧密争抢同企业 leader。
const aiccModelRolloutDeferDelay = 2 * time.Second

// Handle 串行处理企业内落后智能体；任一外部副作用失败都立即返回，绝不启动下一台。
func (h *AICCModelRolloutHandler) Handle(ctx context.Context, job sqlc.Job) error {
	var payload AICCModelRolloutPayload
	if err := json.Unmarshal(job.PayloadJson, &payload); err != nil {
		return fmt.Errorf("解析 aicc_model_rollout payload 失败: %w", err)
	}
	if payload.OrgID == "" || payload.TargetRevision <= 0 {
		return errors.New("aicc_model_rollout payload 缺少有效 org_id 或 target_revision")
	}
	if h.store == nil {
		return aiccRolloutStageError(payload.OrgID, "-", "validate_dependencies", errors.New("未配置持久化 store"))
	}
	if h.orch == nil {
		return aiccRolloutStageError(payload.OrgID, "-", "validate_dependencies", errors.New("Kubernetes 编排器未启用"))
	}
	if h.timeout <= 0 {
		return aiccRolloutStageError(payload.OrgID, "-", "validate_timeout", errors.New("等待超时必须大于 0"))
	}

	for {
		if payload.RepairAgentID == "" {
			if err := h.releaseOwnership(ctx, job.ID, ""); err != nil {
				return err
			}
		}
		// 每台副作用前确认自己是 pending/running 稳定排序 leader。非 leader 无损 defer，
		// 释放 worker 槽且不消耗 attempts；旧 pending 任务恢复后仍保持优先级。
		leader, err := h.store.GetAICCModelRolloutLeaderJob(ctx, json.RawMessage(payload.OrgID))
		if err != nil {
			return aiccRolloutStageError(payload.OrgID, "-", "elect_leader", err)
		}
		if leader.ID != job.ID {
			return &DeferredJobError{Delay: aiccModelRolloutDeferDelay, Reason: fmt.Sprintf("等待同企业 leader job=%s", leader.ID)}
		}
		// marker 优先于最新配置处理：它代表本任务已持有的 app/revision；generation=0 时
		// 先补做 restart，正数时继续核验既有 rollout，均必须先幂等收口。
		if payload.RepairAgentID != "" {
			if err := h.claimOwnership(ctx, job.ID, payload.RepairAppID); err != nil {
				return err
			}
			if err := h.recoverMarkedAgent(ctx, job, &payload); err != nil {
				return err
			}
			continue
		}
		// 每台开始前重新读配置；配置连续修改时，尚未启动的智能体直接追到最新 revision。
		config, err := h.store.GetOrganizationAICCConfig(ctx, payload.OrgID)
		if err != nil {
			return aiccRolloutStageError(payload.OrgID, "-", "load_config", err)
		}
		if config.Revision < payload.TargetRevision {
			return aiccRolloutStageError(payload.OrgID, "-", "merge_revision", fmt.Errorf("当前 revision %d 低于任务目标 %d", config.Revision, payload.TargetRevision))
		}
		targetRevision := config.Revision
		agents, err := h.store.ListPendingAICCModelRolloutAgents(ctx, sqlc.ListPendingAICCModelRolloutAgentsParams{
			OrgID: payload.OrgID, AppliedConfigRevision: targetRevision, Limit: 1,
		})
		if err != nil {
			return aiccRolloutStageError(payload.OrgID, "-", "list_pending", err)
		}
		if len(agents) == 0 {
			return nil
		}
		agent := agents[0]
		if err := h.claimOwnership(ctx, job.ID, agent.AppID); err != nil {
			return err
		}

		// 先持久化任务专属 ownership，再写共享 runtime phase 或触发外部 restart。这样即使
		// 任一副作用后进程崩溃，reconciler 也能从活跃 job 精确识别旧 Ready Pod 不可解闸。
		payload.RepairAgentID = agent.ID
		payload.RepairAppID = agent.AppID
		payload.RepairTargetGeneration = 0
		payload.RepairTargetRevision = targetRevision
		if err := h.persistPayload(ctx, job, payload); err != nil {
			return aiccRolloutStageError(payload.OrgID, agent.ID, "persist_repair_marker", err)
		}
		if err := h.recoverMarkedAgent(ctx, job, &payload); err != nil {
			return err
		}
	}
}

// recoverMarkedAgent 在 generation=0 时执行 restart 并先回写 generation；随后核验并幂等收口。
func (h *AICCModelRolloutHandler) recoverMarkedAgent(ctx context.Context, job sqlc.Job, payload *AICCModelRolloutPayload) error {
	if payload.RepairAppID == "" || payload.RepairTargetGeneration < 0 || payload.RepairTargetRevision <= 0 {
		return aiccRolloutStageError(payload.OrgID, payload.RepairAgentID, "validate_repair_marker", errors.New("任务恢复标记不完整"))
	}
	if payload.RepairTargetGeneration == 0 {
		if err := h.store.SetAppRuntimePhase(ctx, sqlc.SetAppRuntimePhaseParams{ID: payload.RepairAppID, RuntimePhase: domain.RuntimePhaseRestarting}); err != nil {
			return aiccRolloutStageError(payload.OrgID, payload.RepairAgentID, "mark_restarting", err)
		}
		targetGeneration, err := h.orch.RolloutRestartAndGetGeneration(ctx, payload.RepairAppID)
		if err != nil {
			return aiccRolloutStageError(payload.OrgID, payload.RepairAgentID, "restart", err)
		}
		if targetGeneration <= 0 {
			return aiccRolloutStageError(payload.OrgID, payload.RepairAgentID, "restart", fmt.Errorf("返回无效 generation %d", targetGeneration))
		}
		payload.RepairTargetGeneration = targetGeneration
		if err := h.persistPayload(ctx, job, *payload); err != nil {
			return aiccRolloutStageError(payload.OrgID, payload.RepairAgentID, "persist_repair_generation", err)
		}
	}
	if err := h.orch.WaitRolloutReady(ctx, payload.RepairAppID, payload.RepairTargetGeneration, h.timeout, nil); err != nil {
		return aiccRolloutStageError(payload.OrgID, payload.RepairAgentID, "wait_rollout_ready", err)
	}
	return h.finishMarkedAgent(ctx, job, payload)
}

// finishMarkedAgent 按 stamp→ready→clear 顺序收口；marker 直到全部成功才清除。
func (h *AICCModelRolloutHandler) finishMarkedAgent(ctx context.Context, job sqlc.Job, payload *AICCModelRolloutPayload) error {
	if err := h.store.SetAICCAgentAppliedConfigRevision(ctx, sqlc.SetAICCAgentAppliedConfigRevisionParams{
		AppliedConfigRevision: payload.RepairTargetRevision, ID: payload.RepairAgentID, AppliedConfigRevision_2: payload.RepairTargetRevision,
	}); err != nil {
		return aiccRolloutStageError(payload.OrgID, payload.RepairAgentID, "stamp_revision", err)
	}
	rows, err := h.store.SetAppRuntimePhaseReadyForAICCRolloutOwner(ctx, sqlc.SetAppRuntimePhaseReadyForAICCRolloutOwnerParams{ID: payload.RepairAppID, OwnerJobID: job.ID, OwnerJobType: domain.JobTypeAICCModelRollout})
	if err != nil {
		return aiccRolloutStageError(payload.OrgID, payload.RepairAgentID, "mark_ready", err)
	}
	if rows != 1 {
		return aiccRolloutStageError(payload.OrgID, payload.RepairAgentID, "mark_ready", fmt.Errorf("ownership 不再属于当前任务，影响行数=%d", rows))
	}
	agentID := payload.RepairAgentID
	appID := payload.RepairAppID
	payload.RepairAgentID = ""
	payload.RepairAppID = ""
	payload.RepairTargetGeneration = 0
	payload.RepairTargetRevision = 0
	if err := h.persistPayload(ctx, job, *payload); err != nil {
		return aiccRolloutStageError(payload.OrgID, agentID, "clear_repair_marker", err)
	}
	return h.releaseOwnership(ctx, job.ID, appID)
}

// claimOwnership 让模型 rollout 在写 marker 或重启前领取跨类型 app guard，异类 active owner 时 defer。
func (h *AICCModelRolloutHandler) claimOwnership(ctx context.Context, jobID, appID string) error {
	if err := h.store.ClaimAICCRolloutAppOwnership(ctx, sqlc.ClaimAICCRolloutAppOwnershipParams{AppID: appID, OwnerJobID: jobID, OwnerJobType: domain.JobTypeAICCModelRollout}); err != nil {
		return aiccRolloutStageError("-", "-", "claim_ownership", err)
	}
	owner, err := h.store.GetAICCRolloutAppOwnership(ctx, appID)
	if err != nil {
		return aiccRolloutStageError("-", "-", "read_ownership", err)
	}
	if owner.OwnerJobID != jobID || owner.OwnerJobType != domain.JobTypeAICCModelRollout {
		return &DeferredJobError{Delay: aiccModelRolloutDeferDelay, Reason: fmt.Sprintf("等待 app=%s owner job=%s type=%s", appID, owner.OwnerJobID, owner.OwnerJobType)}
	}
	return nil
}

// releaseOwnership 只删除当前模型任务自己的 guard；无 marker 重试先清理上次 clear 后残留记录。
func (h *AICCModelRolloutHandler) releaseOwnership(ctx context.Context, jobID, appID string) error {
	var rows int64
	var err error
	if appID == "" {
		rows, err = h.store.ReleaseAICCRolloutAppOwnershipByOwner(ctx, sqlc.ReleaseAICCRolloutAppOwnershipByOwnerParams{OwnerJobID: jobID, OwnerJobType: domain.JobTypeAICCModelRollout})
	} else {
		rows, err = h.store.ReleaseAICCRolloutAppOwnership(ctx, sqlc.ReleaseAICCRolloutAppOwnershipParams{AppID: appID, OwnerJobID: jobID, OwnerJobType: domain.JobTypeAICCModelRollout})
	}
	if err != nil {
		return aiccRolloutStageError("-", "-", "release_ownership", err)
	}
	if rows > 1 {
		return aiccRolloutStageError("-", "-", "release_ownership", fmt.Errorf("释放 ownership 影响行数异常: %d", rows))
	}
	return nil
}

// persistPayload 原子替换当前 running job payload，保留 org/初始 target 并更新恢复标记。
func (h *AICCModelRolloutHandler) persistPayload(ctx context.Context, job sqlc.Job, payload AICCModelRolloutPayload) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	rows, err := h.store.UpdateJobPayload(ctx, sqlc.UpdateJobPayloadParams{ID: job.ID, PayloadJson: raw, LockedBy: job.LockedBy, LeaseToken: job.LeaseToken})
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("更新 running job payload 影响行数异常: %d", rows)
	}
	return nil
}

// aiccRolloutStageError 统一携带企业、智能体和阶段，保证 worker last_error 可直接定位失败点。
func aiccRolloutStageError(orgID, agentID, stage string, cause error) error {
	return fmt.Errorf("AICC 模型 rollout 失败 org=%s agent=%s stage=%s: %w", orgID, agentID, stage, cause)
}
