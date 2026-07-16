package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/guregu/null/v5"

	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/storage"
	"oc-manager/internal/store/sqlc"
)

const aiccImageMaxBytes int64 = 10 * 1024 * 1024

// aiccPromptInjectionReply 是公开端拦截明确越权指令后的固定答复。
// 固定文本避免模型复述攻击载荷、泄露内部提示词，且不给攻击者提供可迭代的运行时反馈。
const aiccPromptInjectionReply = "该请求包含无法处理的指令内容，请提出产品、价格或售后相关问题。"

const (
	// aiccCreateSessionRateLimit 限制单个来源对单个智能体创建会话频率，防止刷会话。
	aiccCreateSessionRateLimit int64 = 30
	// aiccSendMessageRateLimit 限制单个会话每分钟消息数，防止单会话刷模型消耗。
	aiccSendMessageRateLimit int64 = 20
	// aiccUploadImageRateLimit 限制单个会话每分钟图片上传数。
	aiccUploadImageRateLimit int64 = 10
)

var aiccAllowedImageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
}

// AICCPublicStore 是访客公开 API 依赖的数据访问接口。
type AICCPublicStore interface {
	// GetOrganization 读取企业 AICC 开通状态，公开入口需随平台开关实时下线。
	GetOrganization(ctx context.Context, id string) (sqlc.Organization, error)
	// GetAICCAgent 通过会话内 agent_id 读取未删除智能体，用于消息发送前校验状态。
	GetAICCAgent(ctx context.Context, id string) (sqlc.AiccAgent, error)
	// GetAICCAgentByPublicToken 只通过公开链接 token 定位 active 智能体，避免挂件 token 旁路域名校验。
	GetAICCAgentByPublicToken(ctx context.Context, publicToken string) (sqlc.AiccAgent, error)
	// GetAICCAgentByWidgetToken 只通过网页挂件 token 定位 active 智能体，创建挂件会话时必须继续校验 Origin。
	GetAICCAgentByWidgetToken(ctx context.Context, widgetToken string) (sqlc.AiccAgent, error)
	// GetAICCSessionByToken 通过访客 session token 定位单个公开会话。
	GetAICCSessionByToken(ctx context.Context, token string) (sqlc.AiccSession, error)
	// GetAICCSessionIntent 读取会话级意向事实，用于将留资卡片绑定到首次高意向回复。
	GetAICCSessionIntent(ctx context.Context, sessionID string) (sqlc.AiccSessionIntent, error)
	// LockAICCSessionForUpdate 在事务内锁定会话行，序列化公开消息额度预约。
	LockAICCSessionForUpdate(ctx context.Context, id string) (sqlc.AiccSession, error)
	// GetAICCAgentSettings 读取智能体运营配置；历史智能体可能没有配置行。
	GetAICCAgentSettings(ctx context.Context, agentID string) (sqlc.AiccAgentSetting, error)
	// CountAICCVisitorMessagesBySession 统计当前会话已写入的访客消息数量，用于发送前拦截。
	CountAICCVisitorMessagesBySession(ctx context.Context, sessionID string) (int64, error)
	// LockAICCQueueGovernance 串行化所有副本的全局队列 admission 判断。
	LockAICCQueueGovernance(ctx context.Context) (int8, error)
	// CountActiveAICCMessageTasks 返回仍占据全局队列容量的非终态任务数。
	CountActiveAICCMessageTasks(ctx context.Context) (int64, error)
	// ListAICCMessagesBySession 读取当前会话消息，用于访客刷新页面后恢复对话内容。
	ListAICCMessagesBySession(ctx context.Context, sessionID string) ([]sqlc.AiccMessage, error)
	// ListAICCMessageSources 读取已审计持久化的助手答复来源。
	ListAICCMessageSources(ctx context.Context, messageID string) ([]sqlc.AiccMessageSource, error)
	// GetActiveAICCBlockedVisitor 按匿名访客 hash 查询有效封禁记录，避免公开端继续消耗模型。
	GetActiveAICCBlockedVisitor(ctx context.Context, arg sqlc.GetActiveAICCBlockedVisitorParams) (sqlc.AiccBlockedVisitor, error)
	// CreateAICCSession 创建公开访客会话。
	CreateAICCSession(ctx context.Context, arg sqlc.CreateAICCSessionParams) error
	// MarkAICCSessionConsented 记录访客已同意隐私说明。
	MarkAICCSessionConsented(ctx context.Context, sessionToken string) (int64, error)
	// TouchAICCSessionLastActive 刷新会话活跃时间，公开端续接窗口依赖该时间。
	TouchAICCSessionLastActive(ctx context.Context, id string) (int64, error)
	// CreateAICCMessage 写入访客消息或助手回复镜像。
	CreateAICCMessage(ctx context.Context, arg sqlc.CreateAICCMessageParams) error
	// CreateAICCMessageTask 为访客消息创建异步运行任务；必须与访客消息处于同一事务。
	CreateAICCMessageTask(ctx context.Context, arg sqlc.CreateAICCMessageTaskParams) error
	// GetAICCMessageByClientMessageID 查询同一客户端提交已保留或已完成的消息。
	GetAICCMessageByClientMessageID(ctx context.Context, arg sqlc.GetAICCMessageByClientMessageIDParams) (sqlc.AiccMessage, error)
	// GetAICCMessageTaskByMessageID 查询访客消息对应的异步任务，用于幂等重试返回当前处理状态。
	GetAICCMessageTaskByMessageID(ctx context.Context, messageID string) (sqlc.AiccMessageTask, error)
	// RequeueFailedAICCMessageTask 原子恢复终态失败任务，供同一访客显式重试复用原消息。
	RequeueFailedAICCMessageTask(ctx context.Context, messageID string) (int64, error)
	// GetAICCMessageByID 按消息主键读取访客消息，用于校验状态查询只访问所属会话。
	GetAICCMessageByID(ctx context.Context, id string) (sqlc.AiccMessage, error)
	// GetAICCAssistantMessageByVisitorMessageID 按显式回复关联读取任务产出的助手消息。
	GetAICCAssistantMessageByVisitorMessageID(ctx context.Context, visitorMessageID null.String) (sqlc.AiccMessage, error)
	// CreateAICCImage 写入公开会话图片对象记录。
	CreateAICCImage(ctx context.Context, arg sqlc.CreateAICCImageParams) error
	// GetAICCImageBySession 读取当前会话内已上传图片。
	GetAICCImageBySession(ctx context.Context, arg sqlc.GetAICCImageBySessionParams) (sqlc.AiccImage, error)
	// ListAICCLeadFieldsByAgent 读取智能体留资字段配置，用于访客提交值时校验 field_key。
	ListAICCLeadFieldsByAgent(ctx context.Context, agentID string) ([]sqlc.AiccLeadField, error)
	// UpsertAICCLeadValue 写入或覆盖会话内某个留资字段值。
	UpsertAICCLeadValue(ctx context.Context, arg sqlc.UpsertAICCLeadValueParams) error
	// UpsertAICCLead 按企业和联系方式归并线索主记录，供管理端列表和导出使用。
	UpsertAICCLead(ctx context.Context, arg sqlc.UpsertAICCLeadParams) error
	// GetAICCLeadByContact 读取刚归并的线索主记录，获得稳定 lead_id 用于关联字段值。
	GetAICCLeadByContact(ctx context.Context, arg sqlc.GetAICCLeadByContactParams) (sqlc.AiccLead, error)
	// AttachAICCLeadValuesToLead 把本次会话已提交的字段值关联到归并后的线索主记录。
	AttachAICCLeadValuesToLead(ctx context.Context, arg sqlc.AttachAICCLeadValuesToLeadParams) error
	// ListRequiredAICCLeadFieldsMissing 查询当前会话尚未提交的必填留资字段。
	ListRequiredAICCLeadFieldsMissing(ctx context.Context, sessionID string) ([]sqlc.AiccLeadField, error)
	// UpdateAICCSessionLeadStatus 同步会话留资完成状态。
	UpdateAICCSessionLeadStatus(ctx context.Context, arg sqlc.UpdateAICCSessionLeadStatusParams) error
	// UpdateAICCSessionResolutionStatus 写入会话级解决状态。
	UpdateAICCSessionResolutionStatus(ctx context.Context, arg sqlc.UpdateAICCSessionResolutionStatusParams) error
	// ResetAICCSessionResolutionForNewPhase 仅在已确认状态后记录下一阶段的首条访客消息。
	ResetAICCSessionResolutionForNewPhase(ctx context.Context, arg sqlc.ResetAICCSessionResolutionForNewPhaseParams) error
}

// AICCPublicTxRunner 为公开组合写操作提供事务边界，避免多字段或多表写入半成功。
type AICCPublicTxRunner interface {
	WithAICCPublicTx(ctx context.Context, fn func(AICCPublicStore) error) error
}

// AICCPublicImageBlob 抽象 AICC 公开图片对象存储。
type AICCPublicImageBlob interface {
	PutObject(ctx context.Context, key string, r io.Reader, size int64) error
}

// AICCHermesChat 抽象转发隐藏 app/hermes 的聊天能力。
type AICCHermesChat interface {
	ChatAICC(ctx context.Context, turn AICCInboundTurn) (AICCResponseEnvelope, error)
}

// AICCPublicSessionInput 是访客创建会话时携带的来源信息。
type AICCPublicSessionInput struct {
	// Channel 是入口渠道，默认 web_link。
	Channel string
	// SourceURL 是当前落地页 URL。
	SourceURL string
	// Referrer 是浏览器 referrer。
	Referrer string
	// Origin 是浏览器 Origin 头，网页挂件用它校验允许域名。
	Origin string
	// RemoteIP 是 handler 解析后的客户端 IP，仅做 hash 后保存。
	RemoteIP string
	// UserAgent 是访客浏览器 UA，仅做 hash 后保存。
	UserAgent string
	// SessionToken 是访客端刷新页面时带回的短期会话 token，用于恢复未过期会话。
	SessionToken string
}

// AICCPublicSessionResult 是公开会话创建响应。
type AICCPublicSessionResult struct {
	SessionToken       string `json:"session_token"`
	PrivacyMode        string `json:"privacy_mode"`
	PrivacyText        string `json:"privacy_text,omitempty"`
	PrivacyNoticeShown bool   `json:"privacy_notice_shown"`
	Restored           bool   `json:"restored"`
}

// AICCPublicSessionDetailResult 是访客持有 session token 时可恢复的会话内容。
type AICCPublicSessionDetailResult struct {
	// Messages 是当前公开会话的消息镜像，用于刷新页面后恢复对话内容。
	Messages []AICCMessageResult `json:"messages"`
	// ResolutionStatus 是当前会话级解决状态，公开页刷新后据此恢复“已解决”按钮状态。
	ResolutionStatus string `json:"resolution_status"`
	// LeadStatus 是当前会话的留资完成状态，公开页刷新后据此决定是否继续展示留资表单。
	LeadStatus string `json:"lead_status"`
}

// AICCPublicConfigResult 是公开访客端可读取的智能体展示配置。
type AICCPublicConfigResult struct {
	Name          string                `json:"name"`
	Greeting      string                `json:"greeting,omitempty"`
	PrivacyMode   string                `json:"privacy_mode"`
	PrivacyText   string                `json:"privacy_text,omitempty"`
	RetentionDays int32                 `json:"retention_days"`
	LeadFields    []AICCLeadFieldResult `json:"lead_fields"`
}

// AICCPublicMessageInput 是访客发送消息的入参。
type AICCPublicMessageInput struct {
	SessionToken    string
	ClientMessageID string
	Text            string
	ImageFileID     string
}

// AICCPublicMessageResult 是访客消息的异步受理状态；仅已完成任务才可能携带助手回复文本。
type AICCPublicMessageResult struct {
	// MessageID 是访客消息 ID，也是异步任务的幂等关联键。
	MessageID string `json:"message_id"`
	// Status 为 queued、processing、retry_wait、completed 或 failed。
	Status string `json:"status"`
	// Text 仅在本地拒答或已完成且已持久化助手回复时返回。
	Text string `json:"text,omitempty"`
	// Sources 是当前助手答复已校验的公开来源。
	Sources []AICCResponseSource `json:"sources,omitempty"`
	// NextAction 是 manager 计算后的下一步展示动作。
	NextAction string `json:"next_action,omitempty"`
	// RetryAfterSeconds 仅在 retry_wait 时返回，表示建议客户端下次查询前等待的秒数。
	RetryAfterSeconds *int64 `json:"retry_after_seconds,omitempty"`
}

// AICCPublicImageInput 是访客上传图片的入参。
type AICCPublicImageInput struct {
	SessionToken string
	Filename     string
	Body         io.Reader
	Size         int64
}

// AICCPublicImageResult 是公开图片上传结果，image_file_id 可在发送消息时引用。
type AICCPublicImageResult struct {
	ImageFileID string `json:"image_file_id"`
	Mime        string `json:"mime"`
	Size        int64  `json:"size"`
}

// AICCPublicLeadValuesInput 是访客提交留资字段的入参。
type AICCPublicLeadValuesInput struct {
	SessionToken string
	Values       map[string]string
}

// AICCPublicLeadValuesResult 描述留资提交后的会话留资状态。
type AICCPublicLeadValuesResult struct {
	LeadStatus          string   `json:"lead_status"`
	MissingRequiredKeys []string `json:"missing_required_keys,omitempty"`
}

// AICCPublicResolutionInput 是访客更新当前会话解决状态的入参。
type AICCPublicResolutionInput struct {
	// SessionToken 是当前公开会话 token，仅授权访问单个会话。
	SessionToken string
	// ResolutionStatus 只允许 resolved / unresolved；unknown 是未选择时的默认状态。
	ResolutionStatus string
}

// AICCPublicResolutionResult 描述会话级解决操作后的状态。
type AICCPublicResolutionResult struct {
	ResolutionStatus string `json:"resolution_status"`
}

// aiccPublicSettings 是公开发送链路只需要读取的运营配置子集。
type aiccPublicSettings struct {
	MessageLimitPerSession  int32
	SensitiveWords          []string
	BlockedVisitorEnabled   bool
	SessionResumeTTLMinutes int32
}

// AICCPublicService 负责匿名访客侧 AICC 会话状态机。
type AICCPublicService struct {
	store         AICCPublicStore
	tx            AICCPublicTxRunner
	blob          AICCPublicImageBlob
	chat          AICCHermesChat
	limit         AICCRateLimiter
	geo           AICCGeoIPResolver
	queueCapacity int64
	now           func() time.Time
}

// NewAICCPublicService 创建公开访客服务。
func NewAICCPublicService(store AICCPublicStore, chat AICCHermesChat) *AICCPublicService {
	return &AICCPublicService{store: store, chat: chat, now: time.Now, queueCapacity: 64}
}

// SetTxRunner 注入公开 AICC 写操作事务 runner。
func (s *AICCPublicService) SetTxRunner(tx AICCPublicTxRunner) { s.tx = tx }

// SetImageBlob 注入公开 AICC 图片对象存储；未启用 S3 时图片上传返回不可用。
func (s *AICCPublicService) SetImageBlob(blob AICCPublicImageBlob) { s.blob = blob }

// SetRateLimiter 注入公开匿名入口限流器；未注入时保持兼容，不启用限流。
func (s *AICCPublicService) SetRateLimiter(limiter AICCRateLimiter) { s.limit = limiter }

// SetGeoIPResolver 注入公开会话地域解析器；未注入时地域为空，不影响访客对话。
func (s *AICCPublicService) SetGeoIPResolver(resolver AICCGeoIPResolver) { s.geo = resolver }

// SetQueueCapacity 注入有限的全局持久队列上限；未装配时 SendMessage 以明确错误拒绝写入。
func (s *AICCPublicService) SetQueueCapacity(capacity int64) { s.queueCapacity = capacity }

// PublicConfig 返回访客端可展示的公开智能体配置。
func (s *AICCPublicService) PublicConfig(ctx context.Context, publicToken, channel string) (AICCPublicConfigResult, error) {
	agent, err := s.activeAgentByToken(ctx, publicToken, normalizeAICCChannel(channel))
	if err != nil {
		return AICCPublicConfigResult{}, err
	}
	fields, err := s.store.ListAICCLeadFieldsByAgent(ctx, agent.ID)
	if err != nil {
		return AICCPublicConfigResult{}, fmt.Errorf("查询 AICC 公开留资字段失败: %w", err)
	}
	return AICCPublicConfigResult{
		Name:          agent.Name,
		Greeting:      strOrEmpty(agent.Greeting),
		PrivacyMode:   agent.PrivacyMode,
		PrivacyText:   strOrEmpty(agent.PrivacyText),
		RetentionDays: agent.RetentionDays,
		LeadFields:    toAICCLeadFieldResults(fields),
	}, nil
}

// CreateSession 创建公开访客会话，session token 只授权访问单个会话。
func (s *AICCPublicService) CreateSession(ctx context.Context, publicToken string, input AICCPublicSessionInput) (AICCPublicSessionResult, error) {
	channel := normalizeAICCChannel(input.Channel)
	agent, err := s.activeAgentByToken(ctx, publicToken, channel)
	if err != nil {
		return AICCPublicSessionResult{}, err
	}
	if err := ensureAICCWidgetOriginAllowed(agent, channel, input); err != nil {
		return AICCPublicSessionResult{}, err
	}
	if sessionToken := strings.TrimSpace(input.SessionToken); sessionToken != "" {
		session, err := s.store.GetAICCSessionByToken(ctx, sessionToken)
		if err == nil && session.AgentID == agent.ID && session.ExpiresAt.After(s.now()) {
			settings, err := s.loadPublicSettings(ctx, agent.ID)
			if err != nil {
				return AICCPublicSessionResult{}, err
			}
			if aiccSessionResumeAllowed(session, s.now(), settings.SessionResumeTTLMinutes) {
				return AICCPublicSessionResult{
					SessionToken:       session.SessionToken,
					PrivacyMode:        agent.PrivacyMode,
					PrivacyText:        strOrEmpty(agent.PrivacyText),
					PrivacyNoticeShown: session.PrivacyNoticeShown,
					Restored:           true,
				}, nil
			}
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return AICCPublicSessionResult{}, fmt.Errorf("恢复 AICC 会话失败: %w", err)
		}
	}
	if err := s.ensureRateAllowed(ctx, "create_session:"+agent.ID+":"+hashAICCVisitorMarker(input.RemoteIP), aiccCreateSessionRateLimit, time.Minute); err != nil {
		return AICCPublicSessionResult{}, err
	}
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
		Region:             nullStr(s.resolveAICCRegion(ctx, input.RemoteIP)),
		IpHash:             nullStr(hashAICCVisitorMarker(input.RemoteIP)),
		UserAgentHash:      nullStr(hashAICCVisitorMarker(input.UserAgent)),
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

// GetSession 通过公开 session token 恢复当前访客会话消息。
func (s *AICCPublicService) GetSession(ctx context.Context, sessionToken string) (AICCPublicSessionDetailResult, error) {
	session, err := s.store.GetAICCSessionByToken(ctx, strings.TrimSpace(sessionToken))
	if errors.Is(err, sql.ErrNoRows) {
		return AICCPublicSessionDetailResult{}, ErrAICCInvalidSession
	}
	if err != nil {
		return AICCPublicSessionDetailResult{}, fmt.Errorf("%w: %v", ErrAICCSessionStoreUnavailable, err)
	}
	if !session.ExpiresAt.After(s.now()) {
		return AICCPublicSessionDetailResult{}, ErrAICCInvalidSession
	}
	if _, err := s.activeAgentBySession(ctx, session); err != nil {
		return AICCPublicSessionDetailResult{}, err
	}
	messages, err := s.store.ListAICCMessagesBySession(ctx, session.ID)
	if err != nil {
		return AICCPublicSessionDetailResult{}, fmt.Errorf("查询 AICC 公开会话消息失败: %w", err)
	}
	result := AICCPublicSessionDetailResult{
		Messages:         make([]AICCMessageResult, 0, len(messages)),
		ResolutionStatus: session.ResolutionStatus,
		LeadStatus:       session.LeadStatus,
	}
	for _, row := range messages {
		message := toAICCMessageResult(row)
		if row.Direction == domain.AICCMessageDirectionAssistant {
			sources, sourceErr := s.aiccMessageSources(ctx, row.ID)
			if sourceErr != nil {
				return AICCPublicSessionDetailResult{}, sourceErr
			}
			message.Sources = sources
			message.NextAction = s.aiccAssistantNextAction(ctx, row, session.ResolutionPhaseStartMessageID.String)
			if session.ResolutionStatus != "" && session.ResolutionStatus != domain.AICCResolutionUnknown && message.NextAction == "ask_resolution" {
				message.NextAction = "none"
			}
		}
		if row.Direction == domain.AICCMessageDirectionVisitor {
			task, taskErr := s.aiccMessageTaskResult(ctx, row.ID)
			if taskErr == nil {
				message.TaskStatus = task.Status
				message.RetryAfterSeconds = task.RetryAfterSeconds
			} else if !errors.Is(taskErr, sql.ErrNoRows) {
				return AICCPublicSessionDetailResult{}, fmt.Errorf("查询 AICC 公开消息任务状态失败: %w", taskErr)
			}
		}
		result.Messages = append(result.Messages, message)
	}
	return result, nil
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

// SendMessage 校验公开会话状态、隐私同意与必填留资后原子受理消息；运行时调用由后台任务完成。
func (s *AICCPublicService) SendMessage(ctx context.Context, input AICCPublicMessageInput) (AICCPublicMessageResult, error) {
	session, err := s.store.GetAICCSessionByToken(ctx, strings.TrimSpace(input.SessionToken))
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return AICCPublicMessageResult{}, fmt.Errorf("%w: %v", ErrAICCSessionStoreUnavailable, err)
		}
		return AICCPublicMessageResult{}, ErrAICCInvalidSession
	}
	if !session.ExpiresAt.After(s.now()) {
		return AICCPublicMessageResult{}, ErrAICCInvalidSession
	}
	agent, err := s.activeAgentBySession(ctx, session)
	if err != nil {
		return AICCPublicMessageResult{}, err
	}
	if err := s.ensureRateAllowed(ctx, "send_message:"+session.ID, aiccSendMessageRateLimit, time.Minute); err != nil {
		return AICCPublicMessageResult{}, err
	}
	clientMessageID := strings.TrimSpace(input.ClientMessageID)
	if agent.PrivacyMode == domain.AICCPrivacyModeConsentRequired && !session.PrivacyConsentedAt.Valid {
		return AICCPublicMessageResult{}, ErrAICCConsentRequired
	}
	// 留资仅在访客明确选择提交时校验。客服对话不能因表单尚未填写而被阻断，
	// 否则会把高意向识别误变成强制留资。
	settings, err := s.loadPublicSettings(ctx, session.AgentID)
	if err != nil {
		return AICCPublicMessageResult{}, err
	}
	if settings.BlockedVisitorEnabled {
		if err := s.ensureVisitorNotBlocked(ctx, session); err != nil {
			return AICCPublicMessageResult{}, err
		}
	}
	if clientMessageID != "" {
		existing, err := s.store.GetAICCMessageByClientMessageID(ctx, sqlc.GetAICCMessageByClientMessageIDParams{SessionID: session.ID, Direction: domain.AICCMessageDirectionVisitor, ClientMessageID: nullStr(clientMessageID)})
		if err == nil {
			// 幂等键命中后，重试请求的正文和图片均不参与决策；dispatcher 始终消费已持久化的访客消息。
			existingText := strings.TrimSpace(existing.TextContent.String)
			existingImageObjectKey := strings.TrimSpace(existing.ImageObjectKey.String)
			if containsAICCSensitiveWord(existingText, settings.SensitiveWords) {
				return AICCPublicMessageResult{}, ErrAICCSensitiveWord
			}
			if existingText == "" && existingImageObjectKey == "" {
				return AICCPublicMessageResult{}, fmt.Errorf("%w: 已保存消息内容不能为空", ErrInvalidArgument)
			}
			// 已完成当前会话、风控与持久化内容校验后，才允许终态失败任务恢复；其他状态继续保持幂等读取。
			task, taskErr := s.store.GetAICCMessageTaskByMessageID(ctx, existing.ID)
			if taskErr != nil && !errors.Is(taskErr, sql.ErrNoRows) {
				return AICCPublicMessageResult{}, fmt.Errorf("查询 AICC 幂等任务失败: %w", taskErr)
			}
			if taskErr == nil && task.Status == "failed" {
				if requeueErr := s.requeueFailedTaskWithAdmission(ctx, existing.ID); requeueErr != nil {
					return AICCPublicMessageResult{}, requeueErr
				}
			}
			return s.aiccMessageTaskResultForSession(ctx, existing.ID, session)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return AICCPublicMessageResult{}, fmt.Errorf("查询 AICC 幂等消息失败: %w", err)
		}
	}
	if containsAICCSensitiveWord(input.Text, settings.SensitiveWords) {
		return AICCPublicMessageResult{}, ErrAICCSensitiveWord
	}
	text := strings.TrimSpace(input.Text)
	imageID := strings.TrimSpace(input.ImageFileID)
	var image sqlc.AiccImage
	if imageID != "" {
		image, err = s.store.GetAICCImageBySession(ctx, sqlc.GetAICCImageBySessionParams{ID: imageID, SessionID: session.ID})
		if errors.Is(err, sql.ErrNoRows) {
			return AICCPublicMessageResult{}, fmt.Errorf("%w: 图片不存在", ErrInvalidArgument)
		}
		if err != nil {
			return AICCPublicMessageResult{}, fmt.Errorf("查询 AICC 图片失败: %w", err)
		}
	}
	if text == "" && imageID == "" {
		return AICCPublicMessageResult{}, fmt.Errorf("%w: 消息内容不能为空", ErrInvalidArgument)
	}
	contentType := domain.AICCMessageContentTypeText
	if imageID != "" && text == "" {
		contentType = domain.AICCMessageContentTypeImage
	} else if imageID != "" {
		contentType = domain.AICCMessageContentTypeMixed
	}
	visitorMessage := sqlc.CreateAICCMessageParams{
		ID:              newUUID(),
		SessionID:       session.ID,
		AgentID:         session.AgentID,
		Direction:       domain.AICCMessageDirectionVisitor,
		ContentType:     contentType,
		TextContent:     nullStr(text),
		ImageObjectKey:  nullStr(image.ObjectKey),
		ImageMime:       nullStr(image.Mime),
		ImageSizeBytes:  null.IntFromPtr(int64PtrIfValid(image.SizeBytes, imageID != "")),
		ClientMessageID: nullStr(clientMessageID),
	}
	isPromptInjection := isAICCPromptInjection(text)
	var assistantMessage *sqlc.CreateAICCMessageParams
	if isPromptInjection {
		message := sqlc.CreateAICCMessageParams{ID: newUUID(), SessionID: session.ID, AgentID: session.AgentID, Direction: domain.AICCMessageDirectionAssistant, ContentType: domain.AICCMessageContentTypeText, TextContent: nullStr(aiccPromptInjectionReply), ClientMessageID: nullStr(clientMessageID), ReplyToMessageID: null.StringFrom(visitorMessage.ID), IsRefusal: true}
		assistantMessage = &message
	}
	existingMessageID, err := s.reserveAICCVisitorMessage(ctx, session, settings.MessageLimitPerSession, visitorMessage, agent.AppID, !isPromptInjection, assistantMessage)
	if err != nil {
		return AICCPublicMessageResult{}, err
	}
	if existingMessageID != "" {
		return s.aiccMessageTaskResult(ctx, existingMessageID)
	}
	if !isPromptInjection {
		return AICCPublicMessageResult{MessageID: visitorMessage.ID, Status: "queued"}, nil
	}
	return AICCPublicMessageResult{MessageID: visitorMessage.ID, Status: "completed", Text: aiccPromptInjectionReply}, nil
}

// requeueFailedTaskWithAdmission 在同一事务内取得全局锁、检查容量并恢复失败任务，不能让重试绕过队列上限。
func (s *AICCPublicService) requeueFailedTaskWithAdmission(ctx context.Context, messageID string) error {
	if s.queueCapacity <= 0 {
		return ErrAICCQueueBusy
	}
	return s.withAICCPublicTx(ctx, func(store AICCPublicStore) error {
		if _, err := store.LockAICCQueueGovernance(ctx); err != nil {
			return fmt.Errorf("锁定 AICC 全局队列失败: %w", err)
		}
		active, err := store.CountActiveAICCMessageTasks(ctx)
		if err != nil {
			return fmt.Errorf("统计 AICC 全局队列失败: %w", err)
		}
		if active >= s.queueCapacity {
			return ErrAICCQueueBusy
		}
		if _, err := store.RequeueFailedAICCMessageTask(ctx, messageID); err != nil {
			return fmt.Errorf("恢复失败的 AICC 消息任务失败: %w", err)
		}
		return nil
	})
}

// GetMessageStatus 按 session token 查询一条已受理访客消息的异步处理状态。
// 先验证会话有效性和消息归属，再读取任务，防止其他访客通过枚举消息 ID 观察处理状态。
func (s *AICCPublicService) GetMessageStatus(ctx context.Context, sessionToken, messageID string) (AICCPublicMessageResult, error) {
	session, err := s.store.GetAICCSessionByToken(ctx, strings.TrimSpace(sessionToken))
	if errors.Is(err, sql.ErrNoRows) {
		return AICCPublicMessageResult{}, ErrAICCInvalidSession
	}
	if err != nil {
		return AICCPublicMessageResult{}, fmt.Errorf("%w: %v", ErrAICCSessionStoreUnavailable, err)
	}
	if !session.ExpiresAt.After(s.now()) {
		return AICCPublicMessageResult{}, ErrAICCInvalidSession
	}
	if _, err := s.activeAgentBySession(ctx, session); err != nil {
		return AICCPublicMessageResult{}, err
	}
	message, err := s.store.GetAICCMessageByID(ctx, strings.TrimSpace(messageID))
	if errors.Is(err, sql.ErrNoRows) {
		return AICCPublicMessageResult{}, ErrAICCInvalidMessage
	}
	if err != nil {
		return AICCPublicMessageResult{}, fmt.Errorf("查询 AICC 公开消息失败: %w", err)
	}
	if message.SessionID != session.ID || message.Direction != domain.AICCMessageDirectionVisitor {
		return AICCPublicMessageResult{}, ErrAICCInvalidMessage
	}
	result, err := s.aiccMessageTaskResult(ctx, message.ID)
	if err != nil {
		return AICCPublicMessageResult{}, err
	}
	// 访客已经明确选择结果后，轮询旧消息不能再次弹出解决状态卡片。
	if session.ResolutionStatus != "" && session.ResolutionStatus != domain.AICCResolutionUnknown && result.NextAction == "ask_resolution" {
		result.NextAction = "none"
	}
	if result.Status == "completed" {
		assistant, assistantErr := s.store.GetAICCAssistantMessageByVisitorMessageID(ctx, null.StringFrom(message.ID))
		if assistantErr == nil {
			result.NextAction = s.aiccAssistantNextAction(ctx, assistant, session.ResolutionPhaseStartMessageID.String)
		}
	}
	if session.ResolutionStatus != "" && session.ResolutionStatus != domain.AICCResolutionUnknown && result.NextAction == "ask_resolution" {
		result.NextAction = "none"
	}
	return result, nil
}

func (s *AICCPublicService) reserveAICCVisitorMessage(ctx context.Context, session sqlc.AiccSession, limit int32, visitorMessage sqlc.CreateAICCMessageParams, appID string, createTask bool, assistantMessage *sqlc.CreateAICCMessageParams) (string, error) {
	var existingMessageID string
	err := s.withAICCPublicTx(ctx, func(store AICCPublicStore) error {
		locked, err := store.LockAICCSessionForUpdate(ctx, session.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAICCInvalidSession
		}
		if err != nil {
			return fmt.Errorf("锁定 AICC 会话失败: %w", err)
		}
		if locked.AgentID != session.AgentID {
			return ErrAICCInvalidSession
		}
		if visitorMessage.ClientMessageID.Valid {
			existing, err := store.GetAICCMessageByClientMessageID(ctx, sqlc.GetAICCMessageByClientMessageIDParams{SessionID: session.ID, Direction: domain.AICCMessageDirectionVisitor, ClientMessageID: visitorMessage.ClientMessageID})
			if err == nil {
				existingMessageID = existing.ID
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("查询 AICC 事务内幂等消息失败: %w", err)
			}
		}
		if err := ensureMessageLimit(ctx, store, session.ID, limit); err != nil {
			return err
		}
		// 已确认会话收到新问题即开始新的咨询阶段；状态更新与消息创建同一事务提交。
		if locked.ResolutionStatus == domain.AICCResolutionResolved || locked.ResolutionStatus == domain.AICCResolutionUnresolved {
			if err := store.ResetAICCSessionResolutionForNewPhase(ctx, sqlc.ResetAICCSessionResolutionForNewPhaseParams{ID: locked.ID, ResolutionPhaseStartMessageID: null.StringFrom(visitorMessage.ID)}); err != nil {
				return fmt.Errorf("重置 AICC 会话解决状态失败: %w", err)
			}
		}
		if createTask {
			if s.queueCapacity <= 0 {
				return ErrAICCQueueBusy
			}
			if _, err := store.LockAICCQueueGovernance(ctx); err != nil {
				return fmt.Errorf("锁定 AICC 全局队列失败: %w", err)
			}
			active, err := store.CountActiveAICCMessageTasks(ctx)
			if err != nil {
				return fmt.Errorf("统计 AICC 全局队列失败: %w", err)
			}
			if active >= s.queueCapacity {
				return ErrAICCQueueBusy
			}
		}
		if err := store.CreateAICCMessage(ctx, visitorMessage); err != nil {
			return fmt.Errorf("保存 AICC 访客消息失败: %w", err)
		}
		if createTask {
			// status、attempts 与 max_attempts 使用迁移中的 queued、0、5 默认值；sqlc 参数仅暴露可写列。
			if err := store.CreateAICCMessageTask(ctx, sqlc.CreateAICCMessageTaskParams{ID: newUUID(), MessageID: visitorMessage.ID, SessionID: session.ID, AgentID: session.AgentID, OrgID: session.OrgID, AppID: appID, RunAfter: s.now()}); err != nil {
				return fmt.Errorf("创建 AICC 消息任务失败: %w", err)
			}
		}
		if assistantMessage != nil {
			if err := store.CreateAICCMessage(ctx, *assistantMessage); err != nil {
				return fmt.Errorf("保存 AICC 本地拒答消息失败: %w", err)
			}
		}
		if err := touchAICCSessionLastActive(ctx, store, session.ID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return existingMessageID, nil
}

// aiccMessageTaskResult 将已存在访客消息映射为当前异步任务状态，避免幂等重试重复创建任务。
func (s *AICCPublicService) aiccMessageTaskResult(ctx context.Context, messageID string) (AICCPublicMessageResult, error) {
	task, err := s.store.GetAICCMessageTaskByMessageID(ctx, messageID)
	if err == nil {
		result := AICCPublicMessageResult{MessageID: messageID, Status: task.Status}
		if task.Status == "retry_wait" {
			delay := task.RunAfter.Sub(s.now())
			if delay < 0 {
				delay = 0
			}
			seconds := int64((delay + time.Second - 1) / time.Second)
			if seconds < 1 {
				seconds = 1
			}
			result.RetryAfterSeconds = &seconds
		}
		if task.Status == "completed" {
			assistant, assistantErr := s.store.GetAICCAssistantMessageByVisitorMessageID(ctx, null.StringFrom(messageID))
			if assistantErr == nil {
				result.Text = assistant.TextContent.String
				sources, sourceErr := s.aiccMessageSources(ctx, assistant.ID)
				if sourceErr != nil {
					return AICCPublicMessageResult{}, sourceErr
				}
				result.Sources = sources
				result.NextAction = s.aiccAssistantNextAction(ctx, assistant, "")
			} else if !errors.Is(assistantErr, sql.ErrNoRows) {
				return AICCPublicMessageResult{}, fmt.Errorf("查询 AICC 已完成助手回复失败: %w", assistantErr)
			}
		}
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return AICCPublicMessageResult{}, fmt.Errorf("查询 AICC 幂等任务失败: %w", err)
	}
	assistant, err := s.store.GetAICCAssistantMessageByVisitorMessageID(ctx, null.StringFrom(messageID))
	if err == nil {
		sources, sourceErr := s.aiccMessageSources(ctx, assistant.ID)
		if sourceErr != nil {
			return AICCPublicMessageResult{}, sourceErr
		}
		return AICCPublicMessageResult{MessageID: messageID, Status: "completed", Text: assistant.TextContent.String, Sources: sources, NextAction: s.aiccAssistantNextAction(ctx, assistant, "")}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return AICCPublicMessageResult{}, fmt.Errorf("查询 AICC 幂等助手回复失败: %w", err)
	}
	return AICCPublicMessageResult{}, fmt.Errorf("查询 AICC 幂等任务失败: %w", sql.ErrNoRows)
}

// aiccMessageTaskResultForSession 让幂等重试复用当前会话阶段和解决状态，而不是回放旧卡片动作。
func (s *AICCPublicService) aiccMessageTaskResultForSession(ctx context.Context, messageID string, session sqlc.AiccSession) (AICCPublicMessageResult, error) {
	result, err := s.aiccMessageTaskResult(ctx, messageID)
	if err != nil || result.Status != "completed" {
		return result, err
	}
	assistant, assistantErr := s.store.GetAICCAssistantMessageByVisitorMessageID(ctx, null.StringFrom(messageID))
	if assistantErr == nil {
		result.NextAction = s.aiccAssistantNextAction(ctx, assistant, session.ResolutionPhaseStartMessageID.String)
	}
	if session.ResolutionStatus != "" && session.ResolutionStatus != domain.AICCResolutionUnknown && result.NextAction == "ask_resolution" {
		result.NextAction = "none"
	}
	return result, nil
}

// aiccMessageSources 将数据库来源行转换为公开响应模型；来源已在 dispatcher 写入时完成校验，
// 这里不暴露任何运行时审计字段。
func (s *AICCPublicService) aiccMessageSources(ctx context.Context, messageID string) ([]AICCResponseSource, error) {
	rows, err := s.store.ListAICCMessageSources(ctx, messageID)
	if err != nil {
		return nil, fmt.Errorf("读取 AICC 回复来源失败: %w", err)
	}
	result := make([]AICCResponseSource, 0, len(rows))
	for _, row := range rows {
		result = append(result, AICCResponseSource{Type: row.SourceType, Title: row.Title.String, URL: row.Url.String, Scope: row.Scope.String, ReferenceID: row.ReferenceID.String, Unconfirmed: row.Unconfirmed})
	}
	return result, nil
}

// aiccAssistantNextAction 把下一步动作绑定到当前助手回复。阶段起点只会在已确认咨询后写入；
// unknown 状态下的追问会撤回旧卡片但不会重新计数。
func (s *AICCPublicService) aiccAssistantNextAction(ctx context.Context, assistant sqlc.AiccMessage, phaseStartMessageID string) string {
	if assistant.IsRefusal {
		return "none"
	}
	intent, intentErr := s.store.GetAICCSessionIntent(ctx, assistant.SessionID)
	if intentErr == nil && intent.InviteStatus == "invited" && intent.AnalyzedMessageID.Valid && intent.AnalyzedMessageID.String == assistant.ReplyToMessageID.String {
		return "offer_lead"
	}
	messages, err := s.store.ListAICCMessagesBySession(ctx, assistant.SessionID)
	if err != nil {
		return "none"
	}
	count, targetCount, targetSeen, visitorAfterTarget, phaseStarted := 0, 0, false, false, phaseStartMessageID == ""
	for _, message := range messages {
		if !phaseStarted {
			if message.ID == phaseStartMessageID {
				phaseStarted = true
			}
			continue
		}
		if message.Direction == domain.AICCMessageDirectionVisitor && targetSeen {
			visitorAfterTarget = true
		}
		if message.Direction != domain.AICCMessageDirectionAssistant || message.IsRefusal {
			continue
		}
		count++
		if message.ID == assistant.ID {
			targetSeen = true
			targetCount = count
		}
	}
	if !visitorAfterTarget && targetCount == 2 {
		return "ask_resolution"
	}
	return "none"
}

func (s *AICCPublicService) withAICCPublicTx(ctx context.Context, fn func(AICCPublicStore) error) error {
	if s.tx != nil {
		return s.tx.WithAICCPublicTx(ctx, fn)
	}
	return fn(s.store)
}

func buildAICCRuntimePrompt(agent sqlc.AiccAgent, visitorText string) string {
	lines := []string{
		"你是 AICC（AI Contact Center）在线客服智能体，只能以企业客服身份回答访客问题。",
		"工具白名单：可调用 aicc_knowledge_search；仅在平台网页检索策略允许时调用 web_search 或 web_extract；仅可通过 skills_list、skill_view 使用平台审批的客服 Skill；vision_analyze 仅可理解 manager 已验证且仅属于当前轮的图片；信息不足时可调用 clarify。不得调用或建议调用命令、终端、代码、文件、进程、浏览器操作、发布、定时、Skill 管理或任何写入工具。",
		"涉及企业事实、产品、价格、政策、售后、行业或资料的问题，应先使用 aicc_knowledge_search；不得自行猜测、编写脚本或伪称已执行外部操作。",
		"对上述企业资料类问题，在输出任何最终答复或追问前，必须先调用 aicc_knowledge_search；不得以澄清问题替代首次检索。",
		"知识库命中时必须优先依据命中内容回答；知识库无命中或内容不足时，再说明暂时无法确认并建议访客联系人工客服。",
		"若知识库与企业相关公开网络信息冲突，只采用知识库结论；企业相关公开网络信息必须说明未经企业确认。",
		"最终回复只能输出一个 JSON 对象，且必须严格包含 text、sources、next_action、flags 四个字段：{\"text\":\"\",\"sources\":[],\"next_action\":\"none\",\"flags\":{}}。不得输出 Markdown、解释或 JSON 之外的内容。",
		"sources 只能复用本轮受控工具结果 aicc_response_sources 中完全一致的 type、title、url、scope、unconfirmed 和 reference_id；没有可复用来源时必须输出空数组。next_action 只能为 none、offer_lead 或 ask_resolution。",
		"问题超出业务场景、回答边界或需要人工审批时，应明确说明暂时无法确认，并建议访客联系人工客服。",
	}
	if scenario := strings.TrimSpace(strOrEmpty(agent.Scenario)); scenario != "" {
		lines = append(lines, "业务场景："+scenario)
	}
	if boundary := strings.TrimSpace(strOrEmpty(agent.AnswerBoundary)); boundary != "" {
		lines = append(lines, "回答边界："+boundary)
	}
	lines = append(lines, "访客问题：", strings.TrimSpace(visitorText))
	return strings.Join(lines, "\n")
}

// isAICCPromptInjection 识别公开端常见的提示词窃取与角色覆写指令。
// 仅在“越权动作”与“内部指令目标”同时出现时拦截，避免把正常的产品咨询误判为攻击。
func isAICCPromptInjection(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if normalized == "" {
		return false
	}
	hasDirective := containsAnyAICCText(normalized,
		"忽略此前", "忽略之前", "忽略所有", "无视此前", "无视之前",
		"ignore previous", "ignore all", "disregard previous", "system override",
	)
	hasInternalTarget := containsAnyAICCText(normalized,
		"系统提示词", "系统指令", "开发者指令", "完整提示词",
		"system prompt", "system instruction", "developer message", "developer instruction",
	)
	hasDisclosureAction := containsAnyAICCText(normalized,
		"输出", "显示", "提供", "泄露", "打印", "读取",
		"output", "show", "reveal", "disclose", "print", "read",
	)
	return (hasDirective && hasInternalTarget) || (hasInternalTarget && hasDisclosureAction)
}

// containsAnyAICCText 判断已归一化的访客文本是否包含任一安全规则关键词。
func containsAnyAICCText(text string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(text, candidate) {
			return true
		}
	}
	return false
}

// loadPublicSettings 读取公开发送链路需要的运营配置；历史无配置行时使用管理端同款默认值。
func (s *AICCPublicService) loadPublicSettings(ctx context.Context, agentID string) (aiccPublicSettings, error) {
	settings := defaultAICCPublicSettings()
	row, err := s.store.GetAICCAgentSettings(ctx, agentID)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, nil
	}
	if err != nil {
		return aiccPublicSettings{}, fmt.Errorf("读取 AICC 运营配置失败: %w", err)
	}
	settings, err = publicSettingsFromSQLC(row)
	if err != nil {
		return aiccPublicSettings{}, err
	}
	return settings, nil
}

// defaultAICCPublicSettings 与管理端默认配置保持一致，兼容没有 settings 行的历史智能体。
func defaultAICCPublicSettings() aiccPublicSettings {
	return aiccPublicSettings{
		MessageLimitPerSession:  aiccDefaultMessageLimitPerSession,
		SensitiveWords:          []string{},
		BlockedVisitorEnabled:   true,
		SessionResumeTTLMinutes: aiccDefaultSessionResumeTTLMin,
	}
}

// publicSettingsFromSQLC 将数据库配置行转换为公开发送链路配置，损坏 JSON 直接暴露为服务错误。
func publicSettingsFromSQLC(row sqlc.AiccAgentSetting) (aiccPublicSettings, error) {
	words := []string{}
	if len(bytes.TrimSpace(row.SensitiveWordsJson)) > 0 {
		if err := json.Unmarshal(row.SensitiveWordsJson, &words); err != nil {
			return aiccPublicSettings{}, fmt.Errorf("解析 AICC 敏感词配置失败: %w", err)
		}
		if words == nil {
			words = []string{}
		}
	}
	return aiccPublicSettings{
		MessageLimitPerSession:  row.MessageLimitPerSession,
		SensitiveWords:          normalizeAICCSensitiveWords(words),
		BlockedVisitorEnabled:   row.BlockedVisitorEnabled,
		SessionResumeTTLMinutes: row.SessionResumeTtlMinutes,
	}, nil
}

// aiccSessionResumeAllowed 根据最后活跃时间判断访客刷新是否仍可续接；历史数据缺失时回退到创建时间。
func aiccSessionResumeAllowed(session sqlc.AiccSession, now time.Time, ttlMinutes int32) bool {
	activityAt := session.LastActiveAt
	if activityAt.IsZero() {
		activityAt = session.CreatedAt
	}
	if activityAt.IsZero() || ttlMinutes <= 0 {
		return false
	}
	return !now.After(activityAt.Add(time.Duration(ttlMinutes) * time.Minute))
}

// ensureMessageLimit 在写入新访客消息前检查单会话消息上限，避免超额请求继续进入 Hermes。
func ensureMessageLimit(ctx context.Context, store AICCPublicStore, sessionID string, limit int32) error {
	count, err := store.CountAICCVisitorMessagesBySession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("统计 AICC 会话消息数失败: %w", err)
	}
	if count >= int64(limit) {
		return ErrAICCMessageLimitExceeded
	}
	return nil
}

// touchAICCSessionLastActive 要求刷新命中当前会话；0 行通常表示会话在写入期间过期或被移除。
func touchAICCSessionLastActive(ctx context.Context, store AICCPublicStore, sessionID string) error {
	affected, err := store.TouchAICCSessionLastActive(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("刷新 AICC 会话活跃时间失败: %w", err)
	}
	if affected == 0 {
		return ErrAICCInvalidSession
	}
	return nil
}

// ensureVisitorNotBlocked 仅使用会话中已保存的匿名 hash 查询封禁名单，避免公开端处理原始 IP/UA。
func (s *AICCPublicService) ensureVisitorNotBlocked(ctx context.Context, session sqlc.AiccSession) error {
	for _, visitorHash := range []string{session.IpHash.String, session.UserAgentHash.String} {
		if strings.TrimSpace(visitorHash) == "" {
			continue
		}
		_, err := s.store.GetActiveAICCBlockedVisitor(ctx, sqlc.GetActiveAICCBlockedVisitorParams{
			AgentID:     session.AgentID,
			VisitorHash: visitorHash,
		})
		if err == nil {
			return ErrAICCVisitorBlocked
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("查询 AICC 访客封禁失败: %w", err)
		}
	}
	return nil
}

// containsAICCSensitiveWord 使用简单子串匹配，公开端只做轻量拦截，不引入正则或外部依赖。
func containsAICCSensitiveWord(text string, words []string) bool {
	trimmedText := strings.TrimSpace(text)
	if trimmedText == "" {
		return false
	}
	for _, raw := range words {
		word := strings.TrimSpace(raw)
		if word != "" && strings.Contains(trimmedText, word) {
			return true
		}
	}
	return false
}

// resolveAICCRegion 解析公开访客粗粒度地域；无解析器或库缺失时返回空地域。
func (s *AICCPublicService) resolveAICCRegion(ctx context.Context, remoteIP string) string {
	if s.geo == nil {
		return ""
	}
	region := strings.TrimSpace(s.geo.Resolve(ctx, remoteIP))
	runes := []rune(region)
	if len(runes) > 64 {
		return string(runes[:64])
	}
	return region
}

// resolveAICCRegion 保留旧测试和内部工具的空地域兼容行为。
func resolveAICCRegion(remoteIP string) string {
	_ = remoteIP
	return ""
}

// UploadImage 校验并保存公开访客图片，返回发送消息时可引用的 image_file_id。
func (s *AICCPublicService) UploadImage(ctx context.Context, input AICCPublicImageInput) (AICCPublicImageResult, error) {
	session, err := s.store.GetAICCSessionByToken(ctx, strings.TrimSpace(input.SessionToken))
	if err != nil {
		return AICCPublicImageResult{}, ErrAICCInvalidSession
	}
	if !session.ExpiresAt.After(s.now()) {
		return AICCPublicImageResult{}, ErrAICCInvalidSession
	}
	agent, err := s.activeAgentBySession(ctx, session)
	if err != nil {
		return AICCPublicImageResult{}, err
	}
	if err := s.ensureRateAllowed(ctx, "upload_image:"+session.ID, aiccUploadImageRateLimit, time.Minute); err != nil {
		return AICCPublicImageResult{}, err
	}
	if s.blob == nil {
		return AICCPublicImageResult{}, ErrAICCImageUnavailable
	}
	filename := filepath.Base(input.Filename)
	if filename == "." || filename == ".." || filename == "/" || strings.TrimSpace(filename) == "" {
		return AICCPublicImageResult{}, fmt.Errorf("%w: 图片文件名非法", ErrInvalidArgument)
	}
	if len(filename) > 255 {
		return AICCPublicImageResult{}, fmt.Errorf("%w: 图片文件名过长", ErrInvalidArgument)
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if !aiccAllowedImageExts[ext] {
		return AICCPublicImageResult{}, fmt.Errorf("%w: 图片类型不支持", ErrInvalidArgument)
	}
	if input.Size >= 0 && input.Size > aiccImageMaxBytes {
		return AICCPublicImageResult{}, ErrConversationFileTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(input.Body, aiccImageMaxBytes+1))
	if err != nil {
		return AICCPublicImageResult{}, fmt.Errorf("读取 AICC 图片失败: %w", err)
	}
	if int64(len(data)) > aiccImageMaxBytes {
		return AICCPublicImageResult{}, ErrConversationFileTooLarge
	}
	actualSize := int64(len(data))
	if actualSize == 0 {
		return AICCPublicImageResult{}, fmt.Errorf("%w: 图片内容不能为空", ErrInvalidArgument)
	}
	detected := http.DetectContentType(data)
	mimeType := mime.TypeByExtension(ext)
	if !strings.HasPrefix(mimeType, "image/") {
		return AICCPublicImageResult{}, fmt.Errorf("%w: 图片类型不支持", ErrInvalidArgument)
	}
	if detected != mimeType {
		return AICCPublicImageResult{}, fmt.Errorf("%w: 图片内容类型与扩展名不一致", ErrInvalidArgument)
	}
	imageID := newUUID()
	key := storage.AICCImageKey(agent.AppID, session.ID, imageID, filename)
	if len(key) > 1024 {
		return AICCPublicImageResult{}, fmt.Errorf("%w: 图片对象路径过长", ErrInvalidArgument)
	}
	if err := s.blob.PutObject(ctx, key, bytes.NewReader(data), actualSize); err != nil {
		return AICCPublicImageResult{}, fmt.Errorf("上传 AICC 图片失败: %w", err)
	}
	if err := s.store.CreateAICCImage(ctx, sqlc.CreateAICCImageParams{
		ID:        imageID,
		SessionID: session.ID,
		AgentID:   session.AgentID,
		OrgID:     session.OrgID,
		ObjectKey: key,
		Mime:      mimeType,
		SizeBytes: actualSize,
		Filename:  filename,
	}); err != nil {
		return AICCPublicImageResult{}, fmt.Errorf("保存 AICC 图片记录失败: %w", err)
	}
	return AICCPublicImageResult{ImageFileID: imageID, Mime: mimeType, Size: actualSize}, nil
}

// SubmitLeadValues 写入访客留资字段，并在必填字段齐全后同步会话 lead_status。
func (s *AICCPublicService) SubmitLeadValues(ctx context.Context, input AICCPublicLeadValuesInput) (AICCPublicLeadValuesResult, error) {
	if s.tx != nil {
		var result AICCPublicLeadValuesResult
		err := s.tx.WithAICCPublicTx(ctx, func(store AICCPublicStore) error {
			next := *s
			next.store = store
			var runErr error
			result, runErr = next.submitLeadValues(ctx, input)
			return runErr
		})
		return result, err
	}
	return s.submitLeadValues(ctx, input)
}

func (s *AICCPublicService) submitLeadValues(ctx context.Context, input AICCPublicLeadValuesInput) (AICCPublicLeadValuesResult, error) {
	session, err := s.store.GetAICCSessionByToken(ctx, strings.TrimSpace(input.SessionToken))
	if err != nil {
		return AICCPublicLeadValuesResult{}, ErrAICCInvalidSession
	}
	if !session.ExpiresAt.After(s.now()) {
		return AICCPublicLeadValuesResult{}, ErrAICCInvalidSession
	}
	if _, err := s.activeAgentBySession(ctx, session); err != nil {
		return AICCPublicLeadValuesResult{}, err
	}
	fields, err := s.store.ListAICCLeadFieldsByAgent(ctx, session.AgentID)
	if err != nil {
		return AICCPublicLeadValuesResult{}, fmt.Errorf("查询 AICC 留资字段失败: %w", err)
	}
	if len(input.Values) == 0 {
		return AICCPublicLeadValuesResult{}, fmt.Errorf("%w: 留资字段不能为空", ErrInvalidArgument)
	}
	fieldsByKey := make(map[string]sqlc.AiccLeadField, len(fields))
	for _, field := range fields {
		fieldsByKey[field.FieldKey] = field
	}
	keys := make([]string, 0, len(input.Values))
	for key := range input.Values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, rawKey := range keys {
		key := strings.TrimSpace(rawKey)
		if key == "" {
			return AICCPublicLeadValuesResult{}, fmt.Errorf("%w: 留资字段 key 不能为空", ErrInvalidArgument)
		}
		field, ok := fieldsByKey[key]
		if !ok {
			return AICCPublicLeadValuesResult{}, fmt.Errorf("%w: 未配置的留资字段", ErrInvalidArgument)
		}
		value := strings.TrimSpace(input.Values[rawKey])
		if value == "" {
			return AICCPublicLeadValuesResult{}, fmt.Errorf("%w: 留资字段值不能为空", ErrInvalidArgument)
		}
		if err := s.store.UpsertAICCLeadValue(ctx, sqlc.UpsertAICCLeadValueParams{
			ID:        newUUID(),
			SessionID: session.ID,
			AgentID:   session.AgentID,
			OrgID:     session.OrgID,
			FieldID:   field.ID,
			ValueText: value,
			ValueHash: nullStr(hashAICCLeadValue(value)),
		}); err != nil {
			return AICCPublicLeadValuesResult{}, fmt.Errorf("保存 AICC 留资字段失败: %w", err)
		}
	}
	missing, err := s.store.ListRequiredAICCLeadFieldsMissing(ctx, session.ID)
	if err != nil {
		return AICCPublicLeadValuesResult{}, fmt.Errorf("查询 AICC 必填留资字段失败: %w", err)
	}
	status := domain.AICCLeadStatusPending
	if len(missing) == 0 {
		status = domain.AICCLeadStatusComplete
	}
	// 联系方式是访客主动提交的显式字段，优先级高于模型画像；即使其它可选或必填字段
	// 尚未补齐，也应立即归并正式线索，避免高意向客户在后续刷新前丢失。
	if primaryAICCContactValue(fieldsByKey, input.Values) != "" {
		if err := s.upsertLeadForCompletedSession(ctx, session, fieldsByKey, input.Values); err != nil {
			return AICCPublicLeadValuesResult{}, err
		}
	}
	if err := s.store.UpdateAICCSessionLeadStatus(ctx, sqlc.UpdateAICCSessionLeadStatusParams{
		ID:         session.ID,
		LeadStatus: status,
	}); err != nil {
		return AICCPublicLeadValuesResult{}, fmt.Errorf("更新 AICC 留资状态失败: %w", err)
	}
	// 已提交联系方式后，匿名候选应退出邀约流程；没有意向画像时保持兼容旧会话。
	if intentStore, ok := s.store.(interface {
		UpdateAICCSessionIntentInviteStatus(context.Context, sqlc.UpdateAICCSessionIntentInviteStatusParams) (int64, error)
	}); ok {
		if _, err := intentStore.UpdateAICCSessionIntentInviteStatus(ctx, sqlc.UpdateAICCSessionIntentInviteStatusParams{SessionID: session.ID, InviteStatus: "submitted"}); err != nil {
			return AICCPublicLeadValuesResult{}, fmt.Errorf("更新 AICC 意向邀约状态失败: %w", err)
		}
	}
	return AICCPublicLeadValuesResult{LeadStatus: status, MissingRequiredKeys: aiccLeadFieldKeys(missing)}, nil
}

// DeclineLeadInvitation 仅记录当前访客明确拒绝留资，后续高意向分析不得再次弹出邀约。
func (s *AICCPublicService) DeclineLeadInvitation(ctx context.Context, sessionToken string) error {
	session, err := s.store.GetAICCSessionByToken(ctx, strings.TrimSpace(sessionToken))
	if err != nil || !session.ExpiresAt.After(s.now()) {
		return ErrAICCInvalidSession
	}
	if _, err := s.activeAgentBySession(ctx, session); err != nil {
		return err
	}
	intentStore, ok := s.store.(interface {
		UpdateAICCSessionIntentInviteStatus(context.Context, sqlc.UpdateAICCSessionIntentInviteStatusParams) (int64, error)
	})
	if !ok {
		return ErrAICCSessionStoreUnavailable
	}
	_, err = intentStore.UpdateAICCSessionIntentInviteStatus(ctx, sqlc.UpdateAICCSessionIntentInviteStatusParams{SessionID: session.ID, InviteStatus: "declined"})
	if err != nil {
		return fmt.Errorf("拒绝 AICC 留资邀约失败: %w", err)
	}
	return nil
}

func (s *AICCPublicService) upsertLeadForCompletedSession(ctx context.Context, session sqlc.AiccSession, fieldsByKey map[string]sqlc.AiccLeadField, values map[string]string) error {
	contactValue := primaryAICCContactValue(fieldsByKey, values)
	if contactValue == "" {
		return fmt.Errorf("%w: 缺少可归并的联系方式", ErrInvalidArgument)
	}
	contactHash := hashAICCLeadValue(contactValue)
	if err := s.store.UpsertAICCLead(ctx, sqlc.UpsertAICCLeadParams{
		ID:                 newUUID(),
		OrgID:              session.OrgID,
		PrimaryContactHash: contactHash,
		DisplayName:        nullStr(contactValue),
		LatestSessionID:    null.StringFrom(session.ID),
	}); err != nil {
		return fmt.Errorf("归并 AICC 线索失败: %w", err)
	}
	lead, err := s.store.GetAICCLeadByContact(ctx, sqlc.GetAICCLeadByContactParams{OrgID: session.OrgID, PrimaryContactHash: contactHash})
	if err != nil {
		return fmt.Errorf("读取 AICC 线索失败: %w", err)
	}
	if err := s.store.AttachAICCLeadValuesToLead(ctx, sqlc.AttachAICCLeadValuesToLeadParams{
		LeadID:    null.StringFrom(lead.ID),
		LeadOrgID: null.StringFrom(lead.OrgID),
		SessionID: session.ID,
		OrgID:     session.OrgID,
	}); err != nil {
		return fmt.Errorf("关联 AICC 留资字段失败: %w", err)
	}
	return nil
}

// ResolveSession 将公开访客当前会话标记为已解决。
func (s *AICCPublicService) ResolveSession(ctx context.Context, sessionToken string) (AICCPublicResolutionResult, error) {
	return s.UpdateSessionResolution(ctx, AICCPublicResolutionInput{
		SessionToken:     sessionToken,
		ResolutionStatus: domain.AICCResolutionResolved,
	})
}

// UpdateSessionResolution 将公开访客当前会话标记为已解决或未解决。
func (s *AICCPublicService) UpdateSessionResolution(ctx context.Context, input AICCPublicResolutionInput) (AICCPublicResolutionResult, error) {
	if s.tx != nil {
		var result AICCPublicResolutionResult
		err := s.tx.WithAICCPublicTx(ctx, func(store AICCPublicStore) error {
			next := *s
			next.store = store
			var runErr error
			result, runErr = next.updateSessionResolution(ctx, input)
			return runErr
		})
		return result, err
	}
	return s.updateSessionResolution(ctx, input)
}

func (s *AICCPublicService) updateSessionResolution(ctx context.Context, input AICCPublicResolutionInput) (AICCPublicResolutionResult, error) {
	status := strings.TrimSpace(input.ResolutionStatus)
	switch status {
	case domain.AICCResolutionResolved, domain.AICCResolutionUnresolved:
	default:
		return AICCPublicResolutionResult{}, fmt.Errorf("%w: AICC 会话解决状态非法", ErrInvalidArgument)
	}
	session, err := s.store.GetAICCSessionByToken(ctx, strings.TrimSpace(input.SessionToken))
	if err != nil || !session.ExpiresAt.After(s.now()) {
		return AICCPublicResolutionResult{}, ErrAICCInvalidSession
	}
	if _, err := s.activeAgentBySession(ctx, session); err != nil {
		return AICCPublicResolutionResult{}, err
	}
	if err := s.store.UpdateAICCSessionResolutionStatus(ctx, sqlc.UpdateAICCSessionResolutionStatusParams{
		ID:               session.ID,
		ResolutionStatus: status,
	}); err != nil {
		return AICCPublicResolutionResult{}, fmt.Errorf("更新 AICC 会话解决状态失败: %w", err)
	}
	return AICCPublicResolutionResult{ResolutionStatus: status}, nil
}

func (s *AICCPublicService) activeAgentByToken(ctx context.Context, publicToken, channel string) (sqlc.AiccAgent, error) {
	token := strings.TrimSpace(publicToken)
	if token == "" {
		return sqlc.AiccAgent{}, ErrAICCOffline
	}
	var (
		agent sqlc.AiccAgent
		err   error
	)
	if channel == domain.AICCChannelWebWidget {
		agent, err = s.store.GetAICCAgentByWidgetToken(ctx, token)
	} else {
		agent, err = s.store.GetAICCAgentByPublicToken(ctx, token)
	}
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

func (s *AICCPublicService) activeAgentBySession(ctx context.Context, session sqlc.AiccSession) (sqlc.AiccAgent, error) {
	agent, err := s.store.GetAICCAgent(ctx, session.AgentID)
	if err != nil {
		return sqlc.AiccAgent{}, ErrAICCOffline
	}
	if agent.Status != domain.AICCAgentStatusActive {
		return sqlc.AiccAgent{}, ErrAICCOffline
	}
	if err := s.ensureAICCOrgEnabled(ctx, agent.OrgID); err != nil {
		return sqlc.AiccAgent{}, err
	}
	return agent, nil
}

func (s *AICCPublicService) activeAgentByMessage(ctx context.Context, message sqlc.AiccMessage) (sqlc.AiccAgent, error) {
	agent, err := s.store.GetAICCAgent(ctx, message.AgentID)
	if err != nil {
		return sqlc.AiccAgent{}, ErrAICCInvalidMessage
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

func hashAICCLeadValue(value string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(value))))
	return hex.EncodeToString(sum[:])
}

func hashAICCVisitorMarker(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:])
}

func (s *AICCPublicService) ensureRateAllowed(ctx context.Context, key string, limit int64, window time.Duration) error {
	if s.limit == nil {
		return nil
	}
	allowed, err := s.limit.Allow(ctx, key, limit, window)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAICCRateLimiterUnavailable, err)
	}
	if !allowed {
		return ErrRateLimited
	}
	return nil
}

func ensureAICCWidgetOriginAllowed(agent sqlc.AiccAgent, channel string, input AICCPublicSessionInput) error {
	if channel != domain.AICCChannelWebWidget {
		return nil
	}
	allowed, err := parseAICCAllowedDomains(agent.AllowedDomainsJson)
	if err != nil {
		return fmt.Errorf("%w: AICC 域名白名单配置不合法", ErrInvalidArgument)
	}
	if len(allowed) == 0 {
		return nil
	}
	host := firstAICCRequestHost(input.Origin, input.Referrer, input.SourceURL)
	if host == "" {
		return ErrAICCDomainForbidden
	}
	for _, pattern := range allowed {
		if aiccDomainMatches(pattern, host) {
			return nil
		}
	}
	return ErrAICCDomainForbidden
}

func parseAICCAllowedDomains(raw []byte) ([]string, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		host := normalizeAICCDomainPattern(value)
		if host != "" {
			normalized = append(normalized, host)
		}
	}
	return normalized, nil
}

func firstAICCRequestHost(values ...string) string {
	for _, value := range values {
		if host := normalizeAICCDomainPattern(value); host != "" {
			return strings.TrimPrefix(host, "*.")
		}
	}
	return ""
}

func normalizeAICCDomainPattern(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" {
		return ""
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	host := parsed.Hostname()
	if strings.HasPrefix(strings.TrimSpace(value), "*.") {
		return "*." + strings.TrimPrefix(host, "*.")
	}
	return host
}

func aiccDomainMatches(pattern, host string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	host = strings.ToLower(strings.TrimSpace(host))
	if pattern == "" || host == "" {
		return false
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := strings.TrimPrefix(pattern, "*.")
		return host != suffix && strings.HasSuffix(host, "."+suffix)
	}
	return host == pattern
}

func aiccLeadFieldKeys(fields []sqlc.AiccLeadField) []string {
	if len(fields) == 0 {
		return nil
	}
	keys := make([]string, 0, len(fields))
	for _, field := range fields {
		keys = append(keys, field.FieldKey)
	}
	return keys
}

func primaryAICCContactValue(fieldsByKey map[string]sqlc.AiccLeadField, values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, fieldType := range []string{"phone", "email"} {
		for _, key := range keys {
			field := fieldsByKey[key]
			if field.FieldType == fieldType {
				if value := strings.TrimSpace(values[key]); value != "" {
					return value
				}
			}
		}
	}
	// 非联系方式字段只用于补全匿名候选画像；不能作为跨会话归并依据，避免把企业名称、预算等
	// 普通表单项错误创建为正式线索。
	return ""
}

func int64PtrIfValid(v int64, valid bool) *int64 {
	if !valid {
		return nil
	}
	return &v
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
