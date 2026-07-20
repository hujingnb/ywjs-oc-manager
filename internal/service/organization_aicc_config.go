package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	null "github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// AICCModelValidator 抽象 new-api 实时模型目录，避免企业配置依赖助手版本模型快照。
type AICCModelValidator interface {
	// HasModelInCatalog 判断指定模型 ID 当前是否仍可用。
	HasModelInCatalog(ctx context.Context, id string) (bool, error)
}

// OrganizationAICCConfigStore 描述独立 AICC 配置事务内需要的最小数据访问集合。
type OrganizationAICCConfigStore interface {
	// GetOrganizationAICCConfigForUpdate 锁定企业配置行，串行化 revision 更新。
	GetOrganizationAICCConfigForUpdate(ctx context.Context, orgID string) (sqlc.OrganizationAiccConfig, error)
	// GetOrganizationAICCConfig 读取事务最终配置用于响应。
	GetOrganizationAICCConfig(ctx context.Context, orgID string) (sqlc.OrganizationAiccConfig, error)
	// UpdateOrganizationAICCConfig 写入开关、模型、上限和 revision。
	UpdateOrganizationAICCConfig(ctx context.Context, arg sqlc.UpdateOrganizationAICCConfigParams) error
	// GetIndustryKnowledgeBase 校验授权目标仍存在。
	GetIndustryKnowledgeBase(ctx context.Context, id string) (sqlc.IndustryKnowledgeBasis, error)
	// ReplaceOrganizationIndustryKnowledgeBases 清空旧授权，随后按请求整组重建。
	ReplaceOrganizationIndustryKnowledgeBases(ctx context.Context, orgID string) error
	// AddOrganizationIndustryKnowledgeBase 追加一条已校验授权。
	AddOrganizationIndustryKnowledgeBase(ctx context.Context, arg sqlc.AddOrganizationIndustryKnowledgeBaseParams) (int64, error)
	// DeleteAICCAgentIndustryKnowledgeNotAuthorizedByOrg 清理智能体上已失效的授权关联。
	DeleteAICCAgentIndustryKnowledgeNotAuthorizedByOrg(ctx context.Context, orgID string) error
	// ListOrganizationIndustryKnowledgeBases 回读事务最终行业授权。
	ListOrganizationIndustryKnowledgeBases(ctx context.Context, orgID string) ([]sqlc.IndustryKnowledgeBasis, error)
	// CreateJob 在模型变化时与配置更新原子创建 rollout 任务。
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) error
}

// OrganizationAICCConfigTxRunner 提供企业 AICC 配置整组更新的事务边界。
type OrganizationAICCConfigTxRunner interface {
	WithOrganizationAICCConfigTx(ctx context.Context, fn func(OrganizationAICCConfigStore) error) error
}

// AICCConfigInput 是平台管理员维护企业独立 AICC 配置的入参。
type AICCConfigInput struct {
	// Enabled 表示该企业是否可使用 AICC 子系统。
	Enabled bool
	// Model 是该企业所有 AICC 智能体使用的模型 ID。
	Model string
	// AgentLimit 是智能体数量上限；nil 表示不限。
	AgentLimit *int32
	// IndustryKnowledgeBaseIDs 是平台为企业授权的行业知识库 ID；空数组表示清空授权。
	IndustryKnowledgeBaseIDs []string
}

// OrganizationAICCConfigResult 是独立 GET/PUT 接口返回的完整企业 AICC 配置。
type OrganizationAICCConfigResult struct {
	// OrgID 是配置所属企业 ID。
	OrgID string `json:"org_id"`
	// Enabled 表示企业是否已开通 AICC。
	Enabled bool `json:"enabled"`
	// Model 是企业 AICC 当前模型；关闭且未选择模型时省略。
	Model string `json:"model,omitempty"`
	// AgentLimit 是智能体数量上限；nil 表示不限。
	AgentLimit *int32 `json:"agent_limit,omitempty"`
	// Revision 仅在模型变化时递增，worker 据此判断智能体是否需要 rollout。
	Revision int32 `json:"revision"`
	// IndustryKnowledgeBases 是企业获授权的行业知识库引用。
	IndustryKnowledgeBases []IndustryKnowledgeBaseRef `json:"industry_knowledge_bases"`
}

// SetAICCModelValidator 注入实时模型目录校验器。
func (s *OrganizationService) SetAICCModelValidator(validator AICCModelValidator) {
	s.aiccModelValidator = validator
}

// SetAICCConfigTxRunner 注入独立 AICC 配置事务 runner。
func (s *OrganizationService) SetAICCConfigTxRunner(tx OrganizationAICCConfigTxRunner) {
	s.aiccConfigTx = tx
}

// SetJobNotifier 注入事务提交后的即时任务通知器。
func (s *OrganizationService) SetJobNotifier(notifier JobNotifier) {
	s.jobNotifier = notifier
}

// GetAICCConfig 按角色读取企业独立 AICC 配置；企业管理员仅能读取本企业。
func (s *OrganizationService) GetAICCConfig(ctx context.Context, principal auth.Principal, orgID string) (OrganizationAICCConfigResult, error) {
	if !auth.CanViewAICCConfig(principal, orgID) {
		return OrganizationAICCConfigResult{}, ErrForbidden
	}
	config, err := s.store.GetOrganizationAICCConfig(ctx, orgID)
	if errors.Is(err, sql.ErrNoRows) {
		return OrganizationAICCConfigResult{}, ErrNotFound
	}
	if err != nil {
		return OrganizationAICCConfigResult{}, fmt.Errorf("读取企业 AICC 配置失败: %w", err)
	}
	bases, err := s.store.ListOrganizationIndustryKnowledgeBases(ctx, orgID)
	if err != nil {
		return OrganizationAICCConfigResult{}, fmt.Errorf("读取企业行业知识库授权失败: %w", err)
	}
	return organizationAICCConfigResult(config, bases), nil
}

// UpdateAICCConfig 原子替换企业 AICC 配置和行业授权，并在模型变化时创建 rollout 任务。
func (s *OrganizationService) UpdateAICCConfig(ctx context.Context, principal auth.Principal, orgID string, input AICCConfigInput) (OrganizationAICCConfigResult, error) {
	if !auth.CanManageAICCConfig(principal) {
		return OrganizationAICCConfigResult{}, ErrForbidden
	}
	if input.AgentLimit != nil && *input.AgentLimit < 0 {
		return OrganizationAICCConfigResult{}, fmt.Errorf("%w: AICC 智能体数量上限不能为负数", ErrInvalidArgument)
	}
	model := strings.TrimSpace(input.Model)
	if input.Enabled {
		if model == "" {
			return OrganizationAICCConfigResult{}, fmt.Errorf("%w: 启用 AICC 时必须选择模型", ErrInvalidArgument)
		}
		if s.aiccModelValidator == nil {
			return OrganizationAICCConfigResult{}, fmt.Errorf("AICC 模型目录校验器未配置")
		}
		exists, err := s.aiccModelValidator.HasModelInCatalog(ctx, model)
		if err != nil {
			return OrganizationAICCConfigResult{}, fmt.Errorf("校验 AICC 模型目录失败: %w", err)
		}
		if !exists {
			return OrganizationAICCConfigResult{}, fmt.Errorf("%w: AICC 模型不在实时模型目录中", ErrInvalidArgument)
		}
	}
	industryIDs, err := normalizeAICCKnowledgeIDs(input.IndustryKnowledgeBaseIDs, 20, "行业知识库")
	if err != nil {
		return OrganizationAICCConfigResult{}, err
	}
	if s.aiccConfigTx == nil {
		return OrganizationAICCConfigResult{}, fmt.Errorf("企业 AICC 配置事务未装配")
	}

	var result OrganizationAICCConfigResult
	var rolloutJobID string
	err = s.aiccConfigTx.WithOrganizationAICCConfigTx(ctx, func(txStore OrganizationAICCConfigStore) error {
		current, err := txStore.GetOrganizationAICCConfigForUpdate(ctx, orgID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("锁定企业 AICC 配置失败: %w", err)
		}
		for _, id := range industryIDs {
			if _, err := txStore.GetIndustryKnowledgeBase(ctx, id); errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: 行业知识库不存在", ErrInvalidArgument)
			} else if err != nil {
				return fmt.Errorf("校验行业知识库失败: %w", err)
			}
		}

		modelChanged := strOrEmpty(current.Model) != model
		revision := current.Revision
		if modelChanged {
			revision++
		}
		if err := txStore.UpdateOrganizationAICCConfig(ctx, sqlc.UpdateOrganizationAICCConfigParams{
			Enabled: input.Enabled, Model: null.NewString(model, model != ""), AgentLimit: nullIntFromInt32Ptr(input.AgentLimit),
			Revision: revision, OrgID: orgID,
		}); err != nil {
			return fmt.Errorf("更新企业 AICC 配置失败: %w", err)
		}
		if err := replaceOrganizationAICCIndustryKnowledge(ctx, txStore, orgID, industryIDs); err != nil {
			return err
		}
		if modelChanged {
			rolloutJobID = newUUID()
			payload, err := json.Marshal(struct {
				OrgID          string `json:"org_id"`
				TargetRevision int32  `json:"target_revision"`
			}{OrgID: orgID, TargetRevision: revision})
			if err != nil {
				return fmt.Errorf("编码 AICC 模型发布任务失败: %w", err)
			}
			if err := txStore.CreateJob(ctx, sqlc.CreateJobParams{
				ID: rolloutJobID, Type: domain.JobTypeAICCModelRollout, Priority: 100,
				RunAfter: time.Now().UTC(), MaxAttempts: 20, PayloadJson: payload,
			}); err != nil {
				return fmt.Errorf("创建 AICC 模型发布任务失败: %w", err)
			}
		}
		updated, err := txStore.GetOrganizationAICCConfig(ctx, orgID)
		if err != nil {
			return fmt.Errorf("读取更新后企业 AICC 配置失败: %w", err)
		}
		bases, err := txStore.ListOrganizationIndustryKnowledgeBases(ctx, orgID)
		if err != nil {
			return fmt.Errorf("读取更新后企业行业知识库授权失败: %w", err)
		}
		result = organizationAICCConfigResult(updated, bases)
		return nil
	})
	if err != nil {
		return OrganizationAICCConfigResult{}, err
	}
	if rolloutJobID != "" && s.jobNotifier != nil {
		_ = s.jobNotifier.Enqueue(ctx, rolloutJobID)
	}
	return result, nil
}

// replaceOrganizationAICCIndustryKnowledge 在同一事务中整组替换授权并清理智能体失效关联。
func replaceOrganizationAICCIndustryKnowledge(ctx context.Context, store OrganizationAICCConfigStore, orgID string, ids []string) error {
	if err := store.ReplaceOrganizationIndustryKnowledgeBases(ctx, orgID); err != nil {
		return fmt.Errorf("清空企业行业知识库授权失败: %w", err)
	}
	for _, id := range ids {
		affected, err := store.AddOrganizationIndustryKnowledgeBase(ctx, sqlc.AddOrganizationIndustryKnowledgeBaseParams{OrgID: orgID, IndustryKnowledgeBaseID: id})
		if err != nil {
			return fmt.Errorf("保存企业行业知识库授权失败: %w", err)
		}
		if affected != 1 {
			return fmt.Errorf("%w: 行业知识库不存在", ErrInvalidArgument)
		}
	}
	if err := store.DeleteAICCAgentIndustryKnowledgeNotAuthorizedByOrg(ctx, orgID); err != nil {
		return fmt.Errorf("清理智能体失效行业知识库关联失败: %w", err)
	}
	return nil
}

// organizationAICCConfigResult 把数据库配置和行业库行转换为稳定 API 结果。
func organizationAICCConfigResult(config sqlc.OrganizationAiccConfig, bases []sqlc.IndustryKnowledgeBasis) OrganizationAICCConfigResult {
	result := OrganizationAICCConfigResult{
		OrgID: config.OrgID, Enabled: config.Enabled, Model: strOrEmpty(config.Model),
		AgentLimit: int32PtrFromNullInt(config.AgentLimit), Revision: config.Revision,
		IndustryKnowledgeBases: make([]IndustryKnowledgeBaseRef, 0, len(bases)),
	}
	for _, base := range bases {
		result.IndustryKnowledgeBases = append(result.IndustryKnowledgeBases, IndustryKnowledgeBaseRef{ID: base.ID, Name: base.Name})
	}
	return result
}
