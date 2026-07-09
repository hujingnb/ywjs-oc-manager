package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
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
	// CreateAICCAgent 写入智能体主记录；隐藏 app 由 AICCHiddenAppCreator 先创建。
	CreateAICCAgent(ctx context.Context, arg sqlc.CreateAICCAgentParams) error
	// GetAICCAgent 按 ID 读取未删除智能体。
	GetAICCAgent(ctx context.Context, id string) (sqlc.AiccAgent, error)
	// ListAICCAgentsByOrg 列出企业下未删除智能体。
	ListAICCAgentsByOrg(ctx context.Context, arg sqlc.ListAICCAgentsByOrgParams) ([]sqlc.AiccAgent, error)
	// UpdateAICCAgentProfile 更新智能体可编辑资料。
	UpdateAICCAgentProfile(ctx context.Context, arg sqlc.UpdateAICCAgentProfileParams) error
	// SetAICCAgentStatus 切换智能体运行状态。
	SetAICCAgentStatus(ctx context.Context, arg sqlc.SetAICCAgentStatusParams) error
	// SoftDeleteAICCAgent 软删除智能体，保留历史会话外键。
	SoftDeleteAICCAgent(ctx context.Context, id string) error
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

// AICCService 负责 AICC 智能体管理与隐藏 app 绑定。
type AICCService struct {
	store AICCStore
	apps  AICCHiddenAppCreator
}

// NewAICCService 创建 AICC 管理服务。
func NewAICCService(store AICCStore, apps AICCHiddenAppCreator) *AICCService {
	return &AICCService{store: store, apps: apps}
}

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
	publicToken, err := newAICCToken()
	if err != nil {
		return AICCAgentResult{}, err
	}
	widgetToken, err := newAICCToken()
	if err != nil {
		return AICCAgentResult{}, err
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
		return AICCAgentResult{}, fmt.Errorf("创建 AICC 智能体失败: %w", err)
	}
	row, err := s.getAgentRow(ctx, agentID)
	if err != nil {
		return AICCAgentResult{}, err
	}
	return toAICCAgentResult(row), nil
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
	return nil
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
