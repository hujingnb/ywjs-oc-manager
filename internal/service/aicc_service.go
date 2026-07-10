package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// AICCStore 是 AICC 管理侧依赖的数据访问接口。
type AICCStore interface {
	// GetOrganization 读取企业开通状态、数量上限和版本 allowlist。
	GetOrganization(ctx context.Context, id string) (sqlc.Organization, error)
	// CountAICCAgentsByOrg 统计企业当前未删除智能体数量，用于 aicc_agent_limit 校验。
	CountAICCAgentsByOrg(ctx context.Context, orgID string) (int64, error)
	// CreateAuditLog 写入 AICC 管理动作审计。
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) error
	// CreateAICCAgent 写入智能体主记录；隐藏 app 由 AICCHiddenAppCreator 先创建。
	CreateAICCAgent(ctx context.Context, arg sqlc.CreateAICCAgentParams) error
	// GetAICCAgent 按 ID 读取未删除智能体。
	GetAICCAgent(ctx context.Context, id string) (sqlc.AiccAgent, error)
	// ListAICCAgentsByOrg 列出企业下未删除智能体。
	ListAICCAgentsByOrg(ctx context.Context, arg sqlc.ListAICCAgentsByOrgParams) ([]sqlc.AiccAgent, error)
	// ListAICCAgentKnowledge 列出智能体当前可检索知识范围。
	ListAICCAgentKnowledge(ctx context.Context, agentID string) ([]sqlc.AiccAgentKnowledge, error)
	// DeleteAICCAgentKnowledgeByAgent 清空智能体知识范围，配合 AddAICCAgentKnowledge 整组替换。
	DeleteAICCAgentKnowledgeByAgent(ctx context.Context, agentID string) error
	// AddAICCAgentKnowledge 写入单条知识范围配置。
	AddAICCAgentKnowledge(ctx context.Context, arg sqlc.AddAICCAgentKnowledgeParams) error
	// UpdateAICCAgentProfile 更新智能体可编辑资料。
	UpdateAICCAgentProfile(ctx context.Context, arg sqlc.UpdateAICCAgentProfileParams) error
	// SetAICCAgentStatus 切换智能体运行状态。
	SetAICCAgentStatus(ctx context.Context, arg sqlc.SetAICCAgentStatusParams) error
	// SoftDeleteAICCAgent 软删除智能体，保留历史会话外键。
	SoftDeleteAICCAgent(ctx context.Context, id string) error
	// ListAICCSessionsByAgent 列出指定智能体的访客会话。
	ListAICCSessionsByAgent(ctx context.Context, arg sqlc.ListAICCSessionsByAgentParams) ([]sqlc.AiccSession, error)
	// GetAICCSession 按 ID 读取访客会话详情。
	GetAICCSession(ctx context.Context, id string) (sqlc.AiccSession, error)
	// ListAICCMessagesBySession 列出会话消息镜像。
	ListAICCMessagesBySession(ctx context.Context, sessionID string) ([]sqlc.AiccMessage, error)
	// ListAICCLeadsByOrg 列出企业线索。
	ListAICCLeadsByOrg(ctx context.Context, arg sqlc.ListAICCLeadsByOrgParams) ([]sqlc.AiccLead, error)
	// ListAllAICCLeadsByOrg 导出企业线索，不复用管理列表分页上限，但保留同步导出总量上限。
	ListAllAICCLeadsByOrg(ctx context.Context, arg sqlc.ListAllAICCLeadsByOrgParams) ([]sqlc.AiccLead, error)
	// MarkAICCLeadRead 标记企业线索已读。
	MarkAICCLeadRead(ctx context.Context, arg sqlc.MarkAICCLeadReadParams) (int64, error)
	// ListAICCLeadFieldsByAgent 列出智能体公开页留资字段。
	ListAICCLeadFieldsByAgent(ctx context.Context, agentID string) ([]sqlc.AiccLeadField, error)
	// DeactivateAICCLeadFieldsByAgent 停用智能体全部留资字段；历史留资值仍保留字段锚点。
	DeactivateAICCLeadFieldsByAgent(ctx context.Context, agentID string) error
	// UpsertAICCLeadField 新增或恢复单个留资字段。
	UpsertAICCLeadField(ctx context.Context, arg sqlc.UpsertAICCLeadFieldParams) error
	// CountAICCTodaySessions 统计企业今日会话数。
	CountAICCTodaySessions(ctx context.Context, orgID string) (int64, error)
	// CountAICCUnreadLeads 统计企业未读线索数。
	CountAICCUnreadLeads(ctx context.Context, orgID string) (int64, error)
	// CountAICCSessionsByResolution 统计企业内指定解决状态的会话数。
	CountAICCSessionsByResolution(ctx context.Context, arg sqlc.CountAICCSessionsByResolutionParams) (int64, error)
	// CountAICCCompletedLeadSessions 统计已完成留资的会话数。
	CountAICCCompletedLeadSessions(ctx context.Context, orgID string) (int64, error)
	// ListAICCTopVisitorQuestionsByOrg 统计访客高频问题。
	ListAICCTopVisitorQuestionsByOrg(ctx context.Context, arg sqlc.ListAICCTopVisitorQuestionsByOrgParams) ([]sqlc.ListAICCTopVisitorQuestionsByOrgRow, error)
	// ListAICCTopSourceURLsByOrg 统计访客来源页面分布。
	ListAICCTopSourceURLsByOrg(ctx context.Context, arg sqlc.ListAICCTopSourceURLsByOrgParams) ([]sqlc.ListAICCTopSourceURLsByOrgRow, error)
}

// AICCTxRunner 为管理侧整组保存留资字段提供事务边界。
type AICCTxRunner interface {
	WithAICCTx(ctx context.Context, fn func(AICCStore) error) error
}

// AICCHiddenAppInput 描述创建 AICC 隐藏 app 所需的最小信息。
type AICCHiddenAppInput struct {
	// AppID 是 AICC service 预生成的 app 主键，便于 agent 和 app 绑定可追踪。
	AppID string
	// OrgID 是隐藏 app 所属企业。
	OrgID string
	// UserID 是发起创建的企业管理员，用于审计和语言偏好快照。
	UserID string
	// Name 是隐藏 app 名称，默认跟随智能体名称。
	Name string
}

// AICCHiddenAppCreator 抽象隐藏 app 创建链路，生产实现复用现有 app 初始化能力。
type AICCHiddenAppCreator interface {
	CreateHiddenAICCApp(ctx context.Context, principal auth.Principal, input AICCHiddenAppInput) (string, error)
}

// AICCHiddenAppRollbacker 表示隐藏 app 创建后的补偿清理能力。
// 生产实现使用软删除，避免 AICC 智能体写入失败后留下不可见孤儿 app。
type AICCHiddenAppRollbacker interface {
	SoftDeleteHiddenAICCApp(ctx context.Context, principal auth.Principal, appID string) error
}

// AICCService 负责 AICC 智能体管理与隐藏 app 绑定。
type AICCService struct {
	store AICCStore
	apps  AICCHiddenAppCreator
	tx    AICCTxRunner
}

// NewAICCService 创建 AICC 管理服务。
func NewAICCService(store AICCStore, apps AICCHiddenAppCreator) *AICCService {
	return &AICCService{store: store, apps: apps}
}

// SetTxRunner 注入管理侧事务 runner；未注入时仍可用于轻量单测。
func (s *AICCService) SetTxRunner(tx AICCTxRunner) { s.tx = tx }

// CreateAgent 创建 AICC 智能体并自动创建隐藏 app。
func (s *AICCService) CreateAgent(ctx context.Context, principal auth.Principal, input AICCAgentInput) (AICCAgentResult, error) {
	if principal.OrgID == "" || !auth.CanManageAICCAgent(principal, principal.OrgID) {
		return AICCAgentResult{}, ErrForbidden
	}
	if s.apps == nil {
		return AICCAgentResult{}, fmt.Errorf("AICC 隐藏 app 创建器未配置")
	}
	normalized, err := normalizeAICCAgentInput(input)
	if err != nil {
		return AICCAgentResult{}, err
	}
	org, err := s.store.GetOrganization(ctx, principal.OrgID)
	if errors.Is(err, sql.ErrNoRows) {
		// AICC 创建只能面向 principal 自身企业；主体企业不存在时按不可管理处理，避免泄露租户枚举信息。
		return AICCAgentResult{}, ErrForbidden
	}
	if err != nil {
		return AICCAgentResult{}, fmt.Errorf("查询企业 AICC 配置失败: %w", err)
	}
	if !org.AiccEnabled {
		return AICCAgentResult{}, ErrForbidden
	}
	if err := s.ensureAgentLimit(ctx, org); err != nil {
		return AICCAgentResult{}, err
	}
	agentID := newUUID()
	appID := newUUID()
	publicToken, err := newAICCToken()
	if err != nil {
		return AICCAgentResult{}, err
	}
	widgetToken, err := newAICCToken()
	if err != nil {
		return AICCAgentResult{}, err
	}
	createdAppID, err := s.apps.CreateHiddenAICCApp(ctx, principal, AICCHiddenAppInput{
		AppID:  appID,
		OrgID:  principal.OrgID,
		UserID: principal.UserID,
		Name:   normalized.Name,
	})
	if err != nil {
		return AICCAgentResult{}, fmt.Errorf("创建 AICC 隐藏 app 失败: %w", err)
	}
	if createdAppID != "" {
		appID = createdAppID
	}
	if err := s.store.CreateAICCAgent(ctx, sqlc.CreateAICCAgentParams{
		ID:                 agentID,
		OrgID:              principal.OrgID,
		AppID:              appID,
		Name:               normalized.Name,
		Status:             domain.AICCAgentStatusDraft,
		Scenario:           nullStr(normalized.Scenario),
		Greeting:           nullStr(normalized.Greeting),
		AnswerBoundary:     nullStr(normalized.AnswerBoundary),
		PrivacyMode:        normalized.PrivacyMode,
		PrivacyText:        nullStr(normalized.PrivacyText),
		RetentionDays:      normalized.RetentionDays,
		ThemeJson:          normalized.ThemeJSON,
		AllowedDomainsJson: normalized.AllowedDomainsJSON,
		PublicToken:        publicToken,
		WidgetToken:        widgetToken,
	}); err != nil {
		createErr := fmt.Errorf("创建 AICC 智能体失败: %w", err)
		if rollbackErr := s.rollbackHiddenApp(ctx, principal, appID); rollbackErr != nil {
			return AICCAgentResult{}, errors.Join(createErr, rollbackErr)
		}
		return AICCAgentResult{}, createErr
	}
	row, err := s.getAgentRow(ctx, agentID)
	if err != nil {
		return AICCAgentResult{}, err
	}
	if err := s.recordAICCAudit(ctx, s.store, principal, row.OrgID, row.ID, "create", map[string]any{
		"name":   row.Name,
		"app_id": row.AppID,
	}); err != nil {
		return AICCAgentResult{}, err
	}
	return toAICCAgentResult(row), nil
}

func (s *AICCService) rollbackHiddenApp(ctx context.Context, principal auth.Principal, appID string) error {
	rollbacker, ok := s.apps.(AICCHiddenAppRollbacker)
	if !ok || appID == "" {
		return nil
	}
	if err := rollbacker.SoftDeleteHiddenAICCApp(ctx, principal, appID); err != nil {
		return fmt.Errorf("回滚 AICC 隐藏 app 失败: %w", err)
	}
	return nil
}

// ListAgents 按企业列出智能体；平台管理员必须显式传 orgID，企业管理员可省略使用自身企业。
func (s *AICCService) ListAgents(ctx context.Context, principal auth.Principal, orgID string, limit, offset int32) ([]AICCAgentResult, error) {
	if orgID == "" {
		orgID = principal.OrgID
	}
	if !auth.CanViewAICC(principal, orgID) {
		return nil, ErrForbidden
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.store.ListAICCAgentsByOrg(ctx, sqlc.ListAICCAgentsByOrgParams{OrgID: orgID, Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("查询 AICC 智能体列表失败: %w", err)
	}
	results := make([]AICCAgentResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, toAICCAgentResult(row))
	}
	return results, nil
}

// GetAgent 读取单个智能体，权限使用 CanViewAICC：平台管理员只读、本企业管理员可读。
func (s *AICCService) GetAgent(ctx context.Context, principal auth.Principal, agentID string) (AICCAgentResult, error) {
	row, err := s.getAgentRow(ctx, agentID)
	if err != nil {
		return AICCAgentResult{}, err
	}
	if !auth.CanViewAICC(principal, row.OrgID) {
		return AICCAgentResult{}, ErrForbidden
	}
	return toAICCAgentResult(row), nil
}

// UpdateAgent 更新智能体资料；平台管理员只有读权限，不能管理企业智能体。
func (s *AICCService) UpdateAgent(ctx context.Context, principal auth.Principal, agentID string, input AICCAgentInput) (AICCAgentResult, error) {
	row, err := s.getAgentRow(ctx, agentID)
	if err != nil {
		return AICCAgentResult{}, err
	}
	if !auth.CanManageAICCAgent(principal, row.OrgID) {
		return AICCAgentResult{}, ErrForbidden
	}
	normalized, err := normalizeAICCAgentInput(input)
	if err != nil {
		return AICCAgentResult{}, err
	}
	if err := s.store.UpdateAICCAgentProfile(ctx, sqlc.UpdateAICCAgentProfileParams{
		ID:                 agentID,
		Name:               normalized.Name,
		Scenario:           nullStr(normalized.Scenario),
		Greeting:           nullStr(normalized.Greeting),
		AnswerBoundary:     nullStr(normalized.AnswerBoundary),
		PrivacyMode:        normalized.PrivacyMode,
		PrivacyText:        nullStr(normalized.PrivacyText),
		RetentionDays:      normalized.RetentionDays,
		ThemeJson:          normalized.ThemeJSON,
		AllowedDomainsJson: normalized.AllowedDomainsJSON,
	}); errors.Is(err, sql.ErrNoRows) {
		return AICCAgentResult{}, ErrNotFound
	} else if err != nil {
		return AICCAgentResult{}, fmt.Errorf("更新 AICC 智能体失败: %w", err)
	}
	row, err = s.getAgentRow(ctx, agentID)
	if err != nil {
		return AICCAgentResult{}, err
	}
	if err := s.recordAICCAudit(ctx, s.store, principal, row.OrgID, row.ID, "update", map[string]any{
		"name":           row.Name,
		"privacy_mode":   row.PrivacyMode,
		"retention_days": row.RetentionDays,
	}); err != nil {
		return AICCAgentResult{}, err
	}
	return toAICCAgentResult(row), nil
}

// SetAgentStatus 启动或停止智能体。action 只接受 start / stop，避免 handler 暴露数据库状态细节。
func (s *AICCService) SetAgentStatus(ctx context.Context, principal auth.Principal, agentID, action string) (AICCAgentResult, error) {
	row, err := s.getAgentRow(ctx, agentID)
	if err != nil {
		return AICCAgentResult{}, err
	}
	if !auth.CanManageAICCAgent(principal, row.OrgID) {
		return AICCAgentResult{}, ErrForbidden
	}
	status, err := aiccStatusFromAction(action)
	if err != nil {
		return AICCAgentResult{}, err
	}
	if err := s.store.SetAICCAgentStatus(ctx, sqlc.SetAICCAgentStatusParams{ID: agentID, Status: status}); errors.Is(err, sql.ErrNoRows) {
		return AICCAgentResult{}, ErrNotFound
	} else if err != nil {
		return AICCAgentResult{}, fmt.Errorf("更新 AICC 智能体状态失败: %w", err)
	}
	row, err = s.getAgentRow(ctx, agentID)
	if err != nil {
		return AICCAgentResult{}, err
	}
	if err := s.recordAICCAudit(ctx, s.store, principal, row.OrgID, row.ID, action, map[string]any{
		"status": row.Status,
	}); err != nil {
		return AICCAgentResult{}, err
	}
	return toAICCAgentResult(row), nil
}

// DeleteAgent 软删除智能体；隐藏 app 保留给后续清理任务或审计排查，不在本管理接口直接硬删。
func (s *AICCService) DeleteAgent(ctx context.Context, principal auth.Principal, agentID string) error {
	row, err := s.getAgentRow(ctx, agentID)
	if err != nil {
		return err
	}
	if !auth.CanManageAICCAgent(principal, row.OrgID) {
		return ErrForbidden
	}
	if err := s.store.SoftDeleteAICCAgent(ctx, agentID); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("删除 AICC 智能体失败: %w", err)
	}
	if err := s.recordAICCAudit(ctx, s.store, principal, row.OrgID, row.ID, "delete", map[string]any{
		"name": row.Name,
	}); err != nil {
		return err
	}
	return nil
}

// GetAgentKnowledge 读取智能体知识范围；平台管理员可只读查看，企业管理员可回显配置。
func (s *AICCService) GetAgentKnowledge(ctx context.Context, principal auth.Principal, agentID string) (AICCKnowledgeResult, error) {
	agent, err := s.getAgentRow(ctx, agentID)
	if err != nil {
		return AICCKnowledgeResult{}, err
	}
	if !auth.CanViewAICC(principal, agent.OrgID) {
		return AICCKnowledgeResult{}, ErrForbidden
	}
	rows, err := s.store.ListAICCAgentKnowledge(ctx, agentID)
	if err != nil {
		return AICCKnowledgeResult{}, fmt.Errorf("查询 AICC 知识范围失败: %w", err)
	}
	return toAICCKnowledgeResult(agent, rows), nil
}

// ReplaceAgentKnowledge 整组替换智能体知识范围，避免局部勾选和删除产生不一致配置。
func (s *AICCService) ReplaceAgentKnowledge(ctx context.Context, principal auth.Principal, agentID string, input AICCKnowledgeInput) (AICCKnowledgeResult, error) {
	agent, err := s.getAgentRow(ctx, agentID)
	if err != nil {
		return AICCKnowledgeResult{}, err
	}
	if !auth.CanManageAICCAgent(principal, agent.OrgID) {
		return AICCKnowledgeResult{}, ErrForbidden
	}
	normalized, err := normalizeAICCKnowledgeInput(input)
	if err != nil {
		return AICCKnowledgeResult{}, err
	}
	run := func(store AICCStore) error {
		if err := store.DeleteAICCAgentKnowledgeByAgent(ctx, agentID); err != nil {
			return fmt.Errorf("清空 AICC 知识范围失败: %w", err)
		}
		if normalized.UseOrgKnowledge {
			if err := store.AddAICCAgentKnowledge(ctx, sqlc.AddAICCAgentKnowledgeParams{
				ID:         newUUID(),
				AgentID:    agentID,
				AgentOrgID: agent.OrgID,
				ScopeType:  domain.AICCKnowledgeScopeTypeOrg,
				OrgID:      nullStr(agent.OrgID),
			}); err != nil {
				return fmt.Errorf("保存 AICC 企业知识范围失败: %w", err)
			}
		}
		for _, id := range normalized.IndustryKnowledgeBaseIDs {
			if err := store.AddAICCAgentKnowledge(ctx, sqlc.AddAICCAgentKnowledgeParams{
				ID:                      newUUID(),
				AgentID:                 agentID,
				AgentOrgID:              agent.OrgID,
				ScopeType:               domain.AICCKnowledgeScopeTypeIndustry,
				IndustryKnowledgeBaseID: nullStr(id),
			}); err != nil {
				return fmt.Errorf("保存 AICC 行业知识范围失败: %w", err)
			}
		}
		for _, id := range normalized.AppDocumentIDs {
			if err := store.AddAICCAgentKnowledge(ctx, sqlc.AddAICCAgentKnowledgeParams{
				ID:                newUUID(),
				AgentID:           agentID,
				AgentOrgID:        agent.OrgID,
				ScopeType:         domain.AICCKnowledgeScopeTypeAppDocument,
				OrgID:             nullStr(agent.OrgID),
				AppID:             nullStr(agent.AppID),
				RagflowDocumentID: nullStr(id),
			}); err != nil {
				return fmt.Errorf("保存 AICC 专属文档范围失败: %w", err)
			}
		}
		if err := s.recordAICCAudit(ctx, store, principal, agent.OrgID, agentID, "update_knowledge", map[string]any{
			"use_org_knowledge":           normalized.UseOrgKnowledge,
			"industry_knowledge_base_ids": normalized.IndustryKnowledgeBaseIDs,
			"app_document_ids":            normalized.AppDocumentIDs,
		}); err != nil {
			return err
		}
		return nil
	}
	if s.tx != nil {
		if err := s.tx.WithAICCTx(ctx, run); err != nil {
			return AICCKnowledgeResult{}, err
		}
	} else if err := run(s.store); err != nil {
		return AICCKnowledgeResult{}, err
	}
	return s.GetAgentKnowledge(ctx, principal, agentID)
}

// ListSessions 列出指定智能体的会话摘要；权限先通过智能体归属收敛到企业维度。
func (s *AICCService) ListSessions(ctx context.Context, principal auth.Principal, agentID string, options AICCSessionListOptions) ([]AICCSessionResult, error) {
	agent, err := s.getAgentRow(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if !auth.CanViewAICC(principal, agent.OrgID) {
		return nil, ErrForbidden
	}
	filter, err := normalizeAICCSessionListOptions(options)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListAICCSessionsByAgent(ctx, sqlc.ListAICCSessionsByAgentParams{
		AgentID:          agentID,
		ResolutionStatus: nullStr(filter.ResolutionStatus),
		LeadStatus:       nullStr(filter.LeadStatus),
		Channel:          nullStr(filter.Channel),
		Keyword:          nullStr(filter.Keyword),
		Limit:            filter.Limit,
		Offset:           filter.Offset,
	})
	if err != nil {
		return nil, fmt.Errorf("查询 AICC 会话列表失败: %w", err)
	}
	results := make([]AICCSessionResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, toAICCSessionResult(row))
	}
	return results, nil
}

// GetSession 读取会话详情和消息镜像；平台管理员只读，本企业管理员可读。
func (s *AICCService) GetSession(ctx context.Context, principal auth.Principal, sessionID string) (AICCSessionDetailResult, error) {
	session, err := s.store.GetAICCSession(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return AICCSessionDetailResult{}, ErrNotFound
	}
	if err != nil {
		return AICCSessionDetailResult{}, fmt.Errorf("查询 AICC 会话失败: %w", err)
	}
	if !auth.CanViewAICC(principal, session.OrgID) {
		return AICCSessionDetailResult{}, ErrForbidden
	}
	messages, err := s.store.ListAICCMessagesBySession(ctx, session.ID)
	if err != nil {
		return AICCSessionDetailResult{}, fmt.Errorf("查询 AICC 会话消息失败: %w", err)
	}
	result := AICCSessionDetailResult{Session: toAICCSessionResult(session), Messages: make([]AICCMessageResult, 0, len(messages))}
	for _, row := range messages {
		result.Messages = append(result.Messages, toAICCMessageResult(row))
	}
	return result, nil
}

// ListLeads 列出企业 AICC 线索；orgID 为空时回退当前主体企业。
func (s *AICCService) ListLeads(ctx context.Context, principal auth.Principal, orgID string, limit, offset int32) ([]AICCLeadResult, error) {
	if orgID == "" {
		orgID = principal.OrgID
	}
	if !auth.CanViewAICC(principal, orgID) {
		return nil, ErrForbidden
	}
	limit, offset = normalizeAICCPaging(limit, offset)
	rows, err := s.store.ListAICCLeadsByOrg(ctx, sqlc.ListAICCLeadsByOrgParams{OrgID: orgID, Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("查询 AICC 线索列表失败: %w", err)
	}
	results := make([]AICCLeadResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, toAICCLeadResult(row))
	}
	return results, nil
}

// ExportLeads 导出企业 AICC 全量线索；不复用 ListLeads 的 200 条交互分页上限。
func (s *AICCService) ExportLeads(ctx context.Context, principal auth.Principal, orgID string) ([]AICCLeadResult, error) {
	if orgID == "" {
		orgID = principal.OrgID
	}
	if !auth.CanViewAICC(principal, orgID) {
		return nil, ErrForbidden
	}
	rows, err := s.store.ListAllAICCLeadsByOrg(ctx, sqlc.ListAllAICCLeadsByOrgParams{OrgID: orgID, Limit: aiccLeadExportLimit})
	if err != nil {
		return nil, fmt.Errorf("导出 AICC 线索失败: %w", err)
	}
	results := make([]AICCLeadResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, toAICCLeadResult(row))
	}
	return results, nil
}

// MarkLeadRead 标记线索已读；这是企业运营动作，不向平台只读排障开放写权限。
func (s *AICCService) MarkLeadRead(ctx context.Context, principal auth.Principal, leadID string) error {
	if principal.OrgID == "" || !auth.CanManageAICCAgent(principal, principal.OrgID) {
		return ErrForbidden
	}
	affected, err := s.store.MarkAICCLeadRead(ctx, sqlc.MarkAICCLeadReadParams{ID: leadID, OrgID: principal.OrgID})
	if err != nil {
		return fmt.Errorf("标记 AICC 线索已读失败: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListLeadFields 读取智能体公开页留资字段，供管理端配置面板回显。
func (s *AICCService) ListLeadFields(ctx context.Context, principal auth.Principal, agentID string) ([]AICCLeadFieldResult, error) {
	agent, err := s.getAgentRow(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if !auth.CanViewAICC(principal, agent.OrgID) {
		return nil, ErrForbidden
	}
	rows, err := s.store.ListAICCLeadFieldsByAgent(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("查询 AICC 留资字段失败: %w", err)
	}
	return toAICCLeadFieldResults(rows), nil
}

// ReplaceLeadFields 整组替换智能体留资字段，避免管理端局部编辑产生重复排序或孤儿字段。
func (s *AICCService) ReplaceLeadFields(ctx context.Context, principal auth.Principal, agentID string, inputs []AICCLeadFieldInput) ([]AICCLeadFieldResult, error) {
	agent, err := s.getAgentRow(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if !auth.CanManageAICCAgent(principal, agent.OrgID) {
		return nil, ErrForbidden
	}
	normalized, err := normalizeAICCLeadFields(inputs)
	if err != nil {
		return nil, err
	}
	run := func(store AICCStore) error {
		if err := store.DeactivateAICCLeadFieldsByAgent(ctx, agentID); err != nil {
			return fmt.Errorf("停用 AICC 留资字段失败: %w", err)
		}
		for i, field := range normalized {
			sortOrder := field.SortOrder
			if sortOrder == 0 {
				sortOrder = int32(i + 1)
			}
			if err := store.UpsertAICCLeadField(ctx, sqlc.UpsertAICCLeadFieldParams{
				ID:         newUUID(),
				AgentID:    agentID,
				FieldKey:   field.FieldKey,
				Label:      field.Label,
				FieldType:  field.FieldType,
				Required:   field.Required,
				PromptText: nullStr(field.PromptText),
				SortOrder:  sortOrder,
			}); err != nil {
				return fmt.Errorf("保存 AICC 留资字段失败: %w", err)
			}
		}
		if err := s.recordAICCAudit(ctx, store, principal, agent.OrgID, agentID, "update_lead_fields", map[string]any{
			"field_count": len(normalized),
		}); err != nil {
			return err
		}
		return nil
	}
	if s.tx != nil {
		if err := s.tx.WithAICCTx(ctx, run); err != nil {
			return nil, err
		}
	} else if err := run(s.store); err != nil {
		return nil, err
	}
	return s.ListLeadFields(ctx, principal, agentID)
}

// Analytics 返回 AICC 运营统计卡片数据。
func (s *AICCService) Analytics(ctx context.Context, principal auth.Principal, orgID string) (AICCAnalyticsResult, error) {
	if orgID == "" {
		orgID = principal.OrgID
	}
	if !auth.CanViewAICC(principal, orgID) {
		return AICCAnalyticsResult{}, ErrForbidden
	}
	today, err := s.store.CountAICCTodaySessions(ctx, orgID)
	if err != nil {
		return AICCAnalyticsResult{}, fmt.Errorf("统计 AICC 今日会话失败: %w", err)
	}
	unread, err := s.store.CountAICCUnreadLeads(ctx, orgID)
	if err != nil {
		return AICCAnalyticsResult{}, fmt.Errorf("统计 AICC 未读线索失败: %w", err)
	}
	resolved, err := s.store.CountAICCSessionsByResolution(ctx, sqlc.CountAICCSessionsByResolutionParams{OrgID: orgID, ResolutionStatus: domain.AICCResolutionResolved})
	if err != nil {
		return AICCAnalyticsResult{}, fmt.Errorf("统计 AICC 已解决会话失败: %w", err)
	}
	unresolved, err := s.store.CountAICCSessionsByResolution(ctx, sqlc.CountAICCSessionsByResolutionParams{OrgID: orgID, ResolutionStatus: domain.AICCResolutionUnresolved})
	if err != nil {
		return AICCAnalyticsResult{}, fmt.Errorf("统计 AICC 未解决会话失败: %w", err)
	}
	completedLeads, err := s.store.CountAICCCompletedLeadSessions(ctx, orgID)
	if err != nil {
		return AICCAnalyticsResult{}, fmt.Errorf("统计 AICC 已留资会话失败: %w", err)
	}
	questions, err := s.store.ListAICCTopVisitorQuestionsByOrg(ctx, sqlc.ListAICCTopVisitorQuestionsByOrgParams{OrgID: orgID, Limit: 5})
	if err != nil {
		return AICCAnalyticsResult{}, fmt.Errorf("统计 AICC 热门问题失败: %w", err)
	}
	sources, err := s.store.ListAICCTopSourceURLsByOrg(ctx, sqlc.ListAICCTopSourceURLsByOrgParams{OrgID: orgID, Limit: 5})
	if err != nil {
		return AICCAnalyticsResult{}, fmt.Errorf("统计 AICC 来源页面失败: %w", err)
	}
	return AICCAnalyticsResult{
		TodaySessions:         today,
		UnreadLeads:           unread,
		ResolvedSessions:      resolved,
		UnresolvedSessions:    unresolved,
		CompletedLeadSessions: completedLeads,
		TopQuestions:          toAICCTopQuestionResults(questions),
		TopSources:            toAICCTopSourceResults(sources),
	}, nil
}

func normalizeAICCLeadFields(inputs []AICCLeadFieldInput) ([]AICCLeadFieldInput, error) {
	if len(inputs) > 20 {
		return nil, fmt.Errorf("%w: AICC 留资字段最多 20 个", ErrInvalidArgument)
	}
	seen := make(map[string]struct{}, len(inputs))
	results := make([]AICCLeadFieldInput, 0, len(inputs))
	for _, input := range inputs {
		field := AICCLeadFieldInput{
			FieldKey:   strings.TrimSpace(input.FieldKey),
			Label:      strings.TrimSpace(input.Label),
			FieldType:  strings.TrimSpace(input.FieldType),
			Required:   input.Required,
			PromptText: strings.TrimSpace(input.PromptText),
			SortOrder:  input.SortOrder,
		}
		if field.FieldKey == "" || len(field.FieldKey) > 64 {
			return nil, fmt.Errorf("%w: AICC 留资字段 key 长度非法", ErrInvalidArgument)
		}
		for _, r := range field.FieldKey {
			if !(r == '_' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
				return nil, fmt.Errorf("%w: AICC 留资字段 key 只能包含字母、数字、下划线或短横线", ErrInvalidArgument)
			}
		}
		if _, ok := seen[field.FieldKey]; ok {
			return nil, fmt.Errorf("%w: AICC 留资字段 key 重复", ErrInvalidArgument)
		}
		seen[field.FieldKey] = struct{}{}
		if field.Label == "" || len(field.Label) > 128 {
			return nil, fmt.Errorf("%w: AICC 留资字段名称长度非法", ErrInvalidArgument)
		}
		if field.FieldType == "" {
			field.FieldType = domain.AICCLeadFieldTypeText
		}
		switch field.FieldType {
		case domain.AICCLeadFieldTypeText, domain.AICCLeadFieldTypePhone, domain.AICCLeadFieldTypeEmail, domain.AICCLeadFieldTypeNumber:
		default:
			return nil, fmt.Errorf("%w: AICC 留资字段类型非法", ErrInvalidArgument)
		}
		results = append(results, field)
	}
	return results, nil
}

func normalizeAICCSessionListOptions(options AICCSessionListOptions) (AICCSessionListOptions, error) {
	limit, offset := normalizeAICCPaging(options.Limit, options.Offset)
	normalized := AICCSessionListOptions{
		ResolutionStatus: strings.TrimSpace(options.ResolutionStatus),
		LeadStatus:       strings.TrimSpace(options.LeadStatus),
		Channel:          strings.TrimSpace(options.Channel),
		Keyword:          strings.TrimSpace(options.Keyword),
		Limit:            limit,
		Offset:           offset,
	}
	if normalized.ResolutionStatus != "" {
		switch normalized.ResolutionStatus {
		case domain.AICCResolutionResolved, domain.AICCResolutionUnresolved, domain.AICCResolutionUnknown:
		default:
			return AICCSessionListOptions{}, fmt.Errorf("%w: AICC 会话解决状态非法", ErrInvalidArgument)
		}
	}
	if normalized.LeadStatus != "" {
		switch normalized.LeadStatus {
		case "pending", "complete", "skipped":
		default:
			return AICCSessionListOptions{}, fmt.Errorf("%w: AICC 会话留资状态非法", ErrInvalidArgument)
		}
	}
	if normalized.Channel != "" {
		switch normalized.Channel {
		case domain.AICCChannelWebLink, domain.AICCChannelWebWidget, domain.AICCChannelVoice:
		default:
			return AICCSessionListOptions{}, fmt.Errorf("%w: AICC 会话渠道非法", ErrInvalidArgument)
		}
	}
	if len(normalized.Keyword) > 200 {
		return AICCSessionListOptions{}, fmt.Errorf("%w: AICC 会话搜索关键词过长", ErrInvalidArgument)
	}
	return normalized, nil
}

func (s *AICCService) recordAICCAudit(ctx context.Context, store AICCStore, principal auth.Principal, orgID, agentID, action string, metadata map[string]any) error {
	payload, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("序列化 AICC 审计元数据失败: %w", err)
	}
	if err := store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
		ID:           newUUID(),
		ActorID:      nullStr(principal.UserID),
		ActorRole:    principal.Role,
		OrgID:        nullStr(orgID),
		TargetType:   "aicc_agent",
		TargetID:     agentID,
		Action:       action,
		Result:       "succeeded",
		MetadataJson: payload,
	}); err != nil {
		return fmt.Errorf("写入 AICC 审计日志失败: %w", err)
	}
	return nil
}

func normalizeAICCKnowledgeInput(input AICCKnowledgeInput) (AICCKnowledgeInput, error) {
	industryIDs, err := normalizeAICCKnowledgeIDs(input.IndustryKnowledgeBaseIDs, 20, "行业知识库")
	if err != nil {
		return AICCKnowledgeInput{}, err
	}
	documentIDs, err := normalizeAICCKnowledgeIDs(input.AppDocumentIDs, 200, "专属文档")
	if err != nil {
		return AICCKnowledgeInput{}, err
	}
	return AICCKnowledgeInput{
		UseOrgKnowledge:          input.UseOrgKnowledge,
		IndustryKnowledgeBaseIDs: industryIDs,
		AppDocumentIDs:           documentIDs,
	}, nil
}

func normalizeAICCKnowledgeIDs(ids []string, limit int, label string) ([]string, error) {
	if len(ids) > limit {
		return nil, fmt.Errorf("%w: AICC %s最多选择 %d 个", ErrInvalidArgument, label, limit)
	}
	seen := make(map[string]struct{}, len(ids))
	results := make([]string, 0, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			return nil, fmt.Errorf("%w: AICC %s ID 不能为空", ErrInvalidArgument, label)
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		results = append(results, id)
	}
	return results, nil
}

func (s *AICCService) ensureAgentLimit(ctx context.Context, org sqlc.Organization) error {
	if !org.AiccAgentLimit.Valid {
		return nil
	}
	count, err := s.store.CountAICCAgentsByOrg(ctx, org.ID)
	if err != nil {
		return fmt.Errorf("统计 AICC 智能体数量失败: %w", err)
	}
	if count >= org.AiccAgentLimit.Int64 {
		return fmt.Errorf("%w: AICC 智能体数量已达上限", ErrQuotaExceeded)
	}
	return nil
}

func (s *AICCService) getAgentRow(ctx context.Context, agentID string) (sqlc.AiccAgent, error) {
	row, err := s.store.GetAICCAgent(ctx, agentID)
	if errors.Is(err, sql.ErrNoRows) {
		return sqlc.AiccAgent{}, ErrNotFound
	}
	if err != nil {
		return sqlc.AiccAgent{}, fmt.Errorf("查询 AICC 智能体失败: %w", err)
	}
	return row, nil
}

func normalizeAICCPaging(limit, offset int32) (int32, int32) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func normalizeAICCAgentInput(input AICCAgentInput) (AICCAgentInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return AICCAgentInput{}, fmt.Errorf("%w: AICC 智能体名称不能为空", ErrInvalidArgument)
	}
	if input.RetentionDays == 0 {
		input.RetentionDays = aiccDefaultRetentionDays
	}
	if input.RetentionDays < 1 || input.RetentionDays > aiccMaxRetentionDays {
		return AICCAgentInput{}, fmt.Errorf("%w: AICC 数据保留天数必须在 1 到 3650 之间", ErrInvalidArgument)
	}
	input.PrivacyMode = normalizeAICCPrivacyMode(input.PrivacyMode)
	return input, nil
}

func normalizeAICCPrivacyMode(mode string) string {
	if mode == domain.AICCPrivacyModeConsentRequired {
		return mode
	}
	return domain.AICCPrivacyModeNotice
}

func aiccStatusFromAction(action string) (string, error) {
	switch action {
	case "start":
		return domain.AICCAgentStatusActive, nil
	case "stop":
		return domain.AICCAgentStatusPaused, nil
	default:
		return "", fmt.Errorf("%w: 不支持的 AICC 智能体状态动作", ErrInvalidArgument)
	}
}

func newAICCToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("生成 AICC token 失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func toAICCAgentResult(row sqlc.AiccAgent) AICCAgentResult {
	return AICCAgentResult{
		ID:             row.ID,
		OrgID:          row.OrgID,
		AppID:          row.AppID,
		Name:           row.Name,
		Status:         row.Status,
		Scenario:       strOrEmpty(row.Scenario),
		Greeting:       strOrEmpty(row.Greeting),
		AnswerBoundary: strOrEmpty(row.AnswerBoundary),
		PrivacyMode:    row.PrivacyMode,
		PrivacyText:    strOrEmpty(row.PrivacyText),
		RetentionDays:  row.RetentionDays,
		PublicToken:    row.PublicToken,
		WidgetToken:    row.WidgetToken,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}
}

func toAICCKnowledgeResult(agent sqlc.AiccAgent, rows []sqlc.AiccAgentKnowledge) AICCKnowledgeResult {
	result := AICCKnowledgeResult{
		AgentID:                  agent.ID,
		AppID:                    agent.AppID,
		IndustryKnowledgeBaseIDs: []string{},
		AppDocumentIDs:           []string{},
	}
	for _, row := range rows {
		switch row.ScopeType {
		case domain.AICCKnowledgeScopeTypeOrg:
			result.UseOrgKnowledge = true
		case domain.AICCKnowledgeScopeTypeIndustry:
			if row.IndustryKnowledgeBaseID.Valid {
				result.IndustryKnowledgeBaseIDs = append(result.IndustryKnowledgeBaseIDs, row.IndustryKnowledgeBaseID.String)
			}
		case domain.AICCKnowledgeScopeTypeAppDocument:
			if row.RagflowDocumentID.Valid {
				result.AppDocumentIDs = append(result.AppDocumentIDs, row.RagflowDocumentID.String)
			}
		}
	}
	return result
}

func toAICCSessionResult(row sqlc.AiccSession) AICCSessionResult {
	return AICCSessionResult{
		ID:               row.ID,
		AgentID:          row.AgentID,
		OrgID:            row.OrgID,
		Channel:          row.Channel,
		SourceURL:        strOrEmpty(row.SourceUrl),
		Referrer:         strOrEmpty(row.Referrer),
		ResolutionStatus: row.ResolutionStatus,
		LeadStatus:       row.LeadStatus,
		LastActiveAt:     row.LastActiveAt,
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
	}
}

func toAICCMessageResult(row sqlc.AiccMessage) AICCMessageResult {
	return AICCMessageResult{
		ID:             row.ID,
		Direction:      row.Direction,
		ContentType:    row.ContentType,
		Text:           strOrEmpty(row.TextContent),
		ImageObjectKey: strOrEmpty(row.ImageObjectKey),
		ImageMime:      strOrEmpty(row.ImageMime),
		ImageSizeBytes: row.ImageSizeBytes.Int64,
		IsFallback:     row.IsFallback,
		IsRefusal:      row.IsRefusal,
		ErrorSummary:   strOrEmpty(row.ErrorSummary),
		CreatedAt:      row.CreatedAt,
	}
}

func toAICCLeadResult(row sqlc.AiccLead) AICCLeadResult {
	return AICCLeadResult{
		ID:              row.ID,
		OrgID:           row.OrgID,
		DisplayName:     strOrEmpty(row.DisplayName),
		Unread:          row.Unread,
		LatestSessionID: strOrEmpty(row.LatestSessionID),
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
}

func toAICCLeadFieldResults(rows []sqlc.AiccLeadField) []AICCLeadFieldResult {
	results := make([]AICCLeadFieldResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, AICCLeadFieldResult{
			ID:         row.ID,
			FieldKey:   row.FieldKey,
			Label:      row.Label,
			FieldType:  row.FieldType,
			Required:   row.Required,
			PromptText: strOrEmpty(row.PromptText),
			SortOrder:  row.SortOrder,
		})
	}
	return results
}

func toAICCTopQuestionResults(rows []sqlc.ListAICCTopVisitorQuestionsByOrgRow) []AICCTopItemResult {
	results := make([]AICCTopItemResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, AICCTopItemResult{Label: row.Question, Count: row.Count})
	}
	return results
}

func toAICCTopSourceResults(rows []sqlc.ListAICCTopSourceURLsByOrgRow) []AICCTopItemResult {
	results := make([]AICCTopItemResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, AICCTopItemResult{Label: strOrEmpty(row.SourceUrl), Count: row.Count})
	}
	return results
}
