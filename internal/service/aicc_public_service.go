package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/guregu/null/v5"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// AICCPublicStore 是访客公开 API 依赖的数据访问接口。
type AICCPublicStore interface {
	// GetOrganization 读取企业 AICC 开通状态，公开入口需随平台开关实时下线。
	GetOrganization(ctx context.Context, id string) (sqlc.Organization, error)
	// GetAICCAgent 通过会话内 agent_id 读取未删除智能体，用于消息发送前校验状态。
	GetAICCAgent(ctx context.Context, id string) (sqlc.AiccAgent, error)
	// GetAICCAgentByPublicToken 通过公开链接 token 定位 active 智能体。
	GetAICCAgentByPublicToken(ctx context.Context, token string) (sqlc.AiccAgent, error)
	// GetAICCSessionByToken 通过访客 session token 定位单个公开会话。
	GetAICCSessionByToken(ctx context.Context, token string) (sqlc.AiccSession, error)
	// CreateAICCSession 创建公开访客会话。
	CreateAICCSession(ctx context.Context, arg sqlc.CreateAICCSessionParams) error
	// MarkAICCSessionConsented 记录访客已同意隐私说明。
	MarkAICCSessionConsented(ctx context.Context, sessionToken string) (int64, error)
	// CreateAICCMessage 写入访客消息或助手回复镜像。
	CreateAICCMessage(ctx context.Context, arg sqlc.CreateAICCMessageParams) error
	// ListRequiredAICCLeadFieldsMissing 查询当前会话尚未提交的必填留资字段。
	ListRequiredAICCLeadFieldsMissing(ctx context.Context, sessionID string) ([]sqlc.AiccLeadField, error)
}

// AICCHermesChat 抽象转发隐藏 app/hermes 的聊天能力。
type AICCHermesChat interface {
	ChatAICC(ctx context.Context, appID, sessionID, text string) (string, error)
}

// AICCPublicSessionInput 是访客创建会话时携带的来源信息。
type AICCPublicSessionInput struct {
	// Channel 是入口渠道，默认 web_link。
	Channel string
	// SourceURL 是当前落地页 URL。
	SourceURL string
	// Referrer 是浏览器 referrer。
	Referrer string
}

// AICCPublicSessionResult 是公开会话创建响应。
type AICCPublicSessionResult struct {
	SessionToken       string `json:"session_token"`
	PrivacyMode        string `json:"privacy_mode"`
	PrivacyText        string `json:"privacy_text,omitempty"`
	PrivacyNoticeShown bool   `json:"privacy_notice_shown"`
}

// AICCPublicConfigResult 是公开访客端可读取的智能体展示配置。
type AICCPublicConfigResult struct {
	Name          string `json:"name"`
	Greeting      string `json:"greeting,omitempty"`
	PrivacyMode   string `json:"privacy_mode"`
	PrivacyText   string `json:"privacy_text,omitempty"`
	RetentionDays int32  `json:"retention_days"`
}

// AICCPublicMessageInput 是访客发送消息的入参。
type AICCPublicMessageInput struct {
	SessionToken string
	Text         string
	ImageFileID  string
}

// AICCPublicMessageResult 是访客收到的助手回复。
type AICCPublicMessageResult struct {
	MessageID string `json:"message_id"`
	Text      string `json:"text"`
}

// AICCPublicService 负责匿名访客侧 AICC 会话状态机。
type AICCPublicService struct {
	store AICCPublicStore
	chat  AICCHermesChat
	now   func() time.Time
}

// NewAICCPublicService 创建公开访客服务。
func NewAICCPublicService(store AICCPublicStore, chat AICCHermesChat) *AICCPublicService {
	return &AICCPublicService{store: store, chat: chat, now: time.Now}
}

// PublicConfig 返回访客端可展示的公开智能体配置。
func (s *AICCPublicService) PublicConfig(ctx context.Context, publicToken string) (AICCPublicConfigResult, error) {
	agent, err := s.activeAgentByPublicToken(ctx, publicToken)
	if err != nil {
		return AICCPublicConfigResult{}, err
	}
	return AICCPublicConfigResult{
		Name:          agent.Name,
		Greeting:      strOrEmpty(agent.Greeting),
		PrivacyMode:   agent.PrivacyMode,
		PrivacyText:   strOrEmpty(agent.PrivacyText),
		RetentionDays: agent.RetentionDays,
	}, nil
}

// CreateSession 创建公开访客会话，session token 只授权访问单个会话。
func (s *AICCPublicService) CreateSession(ctx context.Context, publicToken string, input AICCPublicSessionInput) (AICCPublicSessionResult, error) {
	agent, err := s.activeAgentByPublicToken(ctx, publicToken)
	if err != nil {
		return AICCPublicSessionResult{}, err
	}
	channel := normalizeAICCChannel(input.Channel)
	sessionID := newUUID()
	sessionToken, err := newAICCToken()
	if err != nil {
		return AICCPublicSessionResult{}, err
	}
	privacyNoticeShown := agent.PrivacyMode == domain.AICCPrivacyModeNotice
	retentionDays := agent.RetentionDays
	if retentionDays <= 0 {
		retentionDays = aiccDefaultRetentionDays
	}
	if err := s.store.CreateAICCSession(ctx, sqlc.CreateAICCSessionParams{
		ID:                 sessionID,
		AgentID:            agent.ID,
		OrgID:              agent.OrgID,
		SessionToken:       sessionToken,
		Channel:            channel,
		SourceUrl:          null.StringFrom(strings.TrimSpace(input.SourceURL)),
		Referrer:           null.StringFrom(strings.TrimSpace(input.Referrer)),
		PrivacyNoticeShown: privacyNoticeShown,
		ExpiresAt:          s.now().AddDate(0, 0, int(retentionDays)),
	}); err != nil {
		return AICCPublicSessionResult{}, fmt.Errorf("创建 AICC 公开会话失败: %w", err)
	}
	return AICCPublicSessionResult{
		SessionToken:       sessionToken,
		PrivacyMode:        agent.PrivacyMode,
		PrivacyText:        strOrEmpty(agent.PrivacyText),
		PrivacyNoticeShown: privacyNoticeShown,
	}, nil
}

// Consent 记录访客对隐私说明的同意。
func (s *AICCPublicService) Consent(ctx context.Context, sessionToken string) error {
	if strings.TrimSpace(sessionToken) == "" {
		return ErrAICCInvalidSession
	}
	affected, err := s.store.MarkAICCSessionConsented(ctx, strings.TrimSpace(sessionToken))
	if err != nil {
		return fmt.Errorf("记录 AICC 隐私同意失败: %w", err)
	}
	if affected == 0 {
		return ErrAICCInvalidSession
	}
	return nil
}

// SendMessage 校验公开会话状态、隐私同意与必填留资后转发隐藏 app/hermes。
func (s *AICCPublicService) SendMessage(ctx context.Context, input AICCPublicMessageInput) (AICCPublicMessageResult, error) {
	session, err := s.store.GetAICCSessionByToken(ctx, strings.TrimSpace(input.SessionToken))
	if err != nil {
		return AICCPublicMessageResult{}, ErrAICCInvalidSession
	}
	if !session.ExpiresAt.After(s.now()) {
		return AICCPublicMessageResult{}, ErrAICCInvalidSession
	}
	agent, err := s.store.GetAICCAgent(ctx, session.AgentID)
	if err != nil {
		return AICCPublicMessageResult{}, ErrAICCOffline
	}
	if agent.Status != domain.AICCAgentStatusActive {
		return AICCPublicMessageResult{}, ErrAICCOffline
	}
	if err := s.ensureAICCOrgEnabled(ctx, agent.OrgID); err != nil {
		return AICCPublicMessageResult{}, err
	}
	if agent.PrivacyMode == domain.AICCPrivacyModeConsentRequired && !session.PrivacyConsentedAt.Valid {
		return AICCPublicMessageResult{}, ErrAICCConsentRequired
	}
	missing, err := s.store.ListRequiredAICCLeadFieldsMissing(ctx, session.ID)
	if err != nil {
		return AICCPublicMessageResult{}, fmt.Errorf("查询 AICC 必填留资字段失败: %w", err)
	}
	if len(missing) > 0 {
		return AICCPublicMessageResult{}, ErrAICCLeadRequired
	}
	text := strings.TrimSpace(input.Text)
	if strings.TrimSpace(input.ImageFileID) != "" {
		return AICCPublicMessageResult{}, fmt.Errorf("%w: 当前仅支持文字消息", ErrInvalidArgument)
	}
	if text == "" {
		return AICCPublicMessageResult{}, fmt.Errorf("%w: 消息内容不能为空", ErrInvalidArgument)
	}
	visitorMessageID := newUUID()
	if err := s.store.CreateAICCMessage(ctx, sqlc.CreateAICCMessageParams{
		ID:          visitorMessageID,
		SessionID:   session.ID,
		AgentID:     session.AgentID,
		Direction:   domain.AICCMessageDirectionVisitor,
		ContentType: domain.AICCMessageContentTypeText,
		TextContent: nullStr(text),
	}); err != nil {
		return AICCPublicMessageResult{}, fmt.Errorf("保存 AICC 访客消息失败: %w", err)
	}
	reply, err := s.chat.ChatAICC(ctx, agent.AppID, session.ID, text)
	if err != nil {
		return AICCPublicMessageResult{}, fmt.Errorf("转发 AICC 消息失败: %w", err)
	}
	replyID := newUUID()
	if err := s.store.CreateAICCMessage(ctx, sqlc.CreateAICCMessageParams{
		ID:          replyID,
		SessionID:   session.ID,
		AgentID:     session.AgentID,
		Direction:   domain.AICCMessageDirectionAssistant,
		ContentType: domain.AICCMessageContentTypeText,
		TextContent: nullStr(reply),
	}); err != nil {
		return AICCPublicMessageResult{}, fmt.Errorf("保存 AICC 助手回复失败: %w", err)
	}
	return AICCPublicMessageResult{MessageID: replyID, Text: reply}, nil
}

func (s *AICCPublicService) activeAgentByPublicToken(ctx context.Context, publicToken string) (sqlc.AiccAgent, error) {
	token := strings.TrimSpace(publicToken)
	if token == "" {
		return sqlc.AiccAgent{}, ErrAICCOffline
	}
	agent, err := s.store.GetAICCAgentByPublicToken(ctx, token)
	if errors.Is(err, sql.ErrNoRows) {
		return sqlc.AiccAgent{}, ErrAICCOffline
	}
	if err != nil {
		return sqlc.AiccAgent{}, fmt.Errorf("查询 AICC 公开智能体失败: %w", err)
	}
	if agent.Status != domain.AICCAgentStatusActive {
		return sqlc.AiccAgent{}, ErrAICCOffline
	}
	if err := s.ensureAICCOrgEnabled(ctx, agent.OrgID); err != nil {
		return sqlc.AiccAgent{}, err
	}
	return agent, nil
}

func (s *AICCPublicService) ensureAICCOrgEnabled(ctx context.Context, orgID string) error {
	org, err := s.store.GetOrganization(ctx, orgID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAICCOffline
	}
	if err != nil {
		return fmt.Errorf("查询 AICC 企业开通状态失败: %w", err)
	}
	if !org.AiccEnabled {
		return ErrAICCOffline
	}
	return nil
}

func normalizeAICCChannel(channel string) string {
	switch strings.TrimSpace(channel) {
	case domain.AICCChannelWebWidget:
		return domain.AICCChannelWebWidget
	case domain.AICCChannelVoice:
		return domain.AICCChannelVoice
	default:
		return domain.AICCChannelWebLink
	}
}
