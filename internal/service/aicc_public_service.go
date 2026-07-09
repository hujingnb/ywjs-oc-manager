package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
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

var aiccAllowedImageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
}

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
	// CreateAICCImage 写入公开会话图片对象记录。
	CreateAICCImage(ctx context.Context, arg sqlc.CreateAICCImageParams) error
	// GetAICCImageBySession 读取当前会话内已上传图片。
	GetAICCImageBySession(ctx context.Context, arg sqlc.GetAICCImageBySessionParams) (sqlc.AiccImage, error)
	// ListAICCLeadFieldsByAgent 读取智能体留资字段配置，用于访客提交值时校验 field_key。
	ListAICCLeadFieldsByAgent(ctx context.Context, agentID string) ([]sqlc.AiccLeadField, error)
	// UpsertAICCLeadValue 写入或覆盖会话内某个留资字段值。
	UpsertAICCLeadValue(ctx context.Context, arg sqlc.UpsertAICCLeadValueParams) error
	// ListRequiredAICCLeadFieldsMissing 查询当前会话尚未提交的必填留资字段。
	ListRequiredAICCLeadFieldsMissing(ctx context.Context, sessionID string) ([]sqlc.AiccLeadField, error)
	// UpdateAICCSessionLeadStatus 同步会话留资完成状态。
	UpdateAICCSessionLeadStatus(ctx context.Context, arg sqlc.UpdateAICCSessionLeadStatusParams) error
	// GetAICCAssistantMessageForFeedback 查询可反馈的未过期助手消息。
	GetAICCAssistantMessageForFeedback(ctx context.Context, arg sqlc.GetAICCAssistantMessageForFeedbackParams) (sqlc.AiccMessage, error)
	// UpsertAICCFeedback 写入或覆盖单条助手消息反馈。
	UpsertAICCFeedback(ctx context.Context, arg sqlc.UpsertAICCFeedbackParams) error
	// UpdateAICCSessionResolutionStatus 根据反馈同步会话解决状态。
	UpdateAICCSessionResolutionStatus(ctx context.Context, arg sqlc.UpdateAICCSessionResolutionStatusParams) error
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

// AICCPublicFeedbackInput 是访客提交助手回复反馈的入参。
type AICCPublicFeedbackInput struct {
	SessionToken string
	MessageID    string
	Helpful      bool
}

// AICCPublicFeedbackResult 描述反馈提交后的会话解决状态。
type AICCPublicFeedbackResult struct {
	ResolutionStatus string `json:"resolution_status"`
}

// AICCPublicService 负责匿名访客侧 AICC 会话状态机。
type AICCPublicService struct {
	store AICCPublicStore
	tx    AICCPublicTxRunner
	blob  AICCPublicImageBlob
	chat  AICCHermesChat
	now   func() time.Time
}

// NewAICCPublicService 创建公开访客服务。
func NewAICCPublicService(store AICCPublicStore, chat AICCHermesChat) *AICCPublicService {
	return &AICCPublicService{store: store, chat: chat, now: time.Now}
}

// SetTxRunner 注入公开 AICC 写操作事务 runner。
func (s *AICCPublicService) SetTxRunner(tx AICCPublicTxRunner) { s.tx = tx }

// SetImageBlob 注入公开 AICC 图片对象存储；未启用 S3 时图片上传返回不可用。
func (s *AICCPublicService) SetImageBlob(blob AICCPublicImageBlob) { s.blob = blob }

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
	agent, err := s.activeAgentBySession(ctx, session)
	if err != nil {
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
	visitorMessageID := newUUID()
	if err := s.store.CreateAICCMessage(ctx, sqlc.CreateAICCMessageParams{
		ID:             visitorMessageID,
		SessionID:      session.ID,
		AgentID:        session.AgentID,
		Direction:      domain.AICCMessageDirectionVisitor,
		ContentType:    contentType,
		TextContent:    nullStr(text),
		ImageObjectKey: nullStr(image.ObjectKey),
		ImageMime:      nullStr(image.Mime),
		ImageSizeBytes: null.IntFromPtr(int64PtrIfValid(image.SizeBytes, imageID != "")),
	}); err != nil {
		return AICCPublicMessageResult{}, fmt.Errorf("保存 AICC 访客消息失败: %w", err)
	}
	runtimeText := text
	if runtimeText == "" && imageID != "" {
		runtimeText = "[访客发送了一张图片]"
	}
	reply, err := s.chat.ChatAICC(ctx, agent.AppID, session.ID, runtimeText)
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

// UploadImage 校验并保存公开访客图片，返回发送消息时可引用的 image_file_id。
func (s *AICCPublicService) UploadImage(ctx context.Context, input AICCPublicImageInput) (AICCPublicImageResult, error) {
	if s.blob == nil {
		return AICCPublicImageResult{}, ErrAICCImageUnavailable
	}
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
	filename := filepath.Base(input.Filename)
	if filename == "." || filename == ".." || filename == "/" || strings.TrimSpace(filename) == "" {
		return AICCPublicImageResult{}, fmt.Errorf("%w: 图片文件名非法", ErrInvalidArgument)
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
	if detected := http.DetectContentType(data); !strings.HasPrefix(detected, "image/") {
		return AICCPublicImageResult{}, fmt.Errorf("%w: 图片内容类型不支持", ErrInvalidArgument)
	}
	mimeType := mime.TypeByExtension(ext)
	if !strings.HasPrefix(mimeType, "image/") {
		return AICCPublicImageResult{}, fmt.Errorf("%w: 图片类型不支持", ErrInvalidArgument)
	}
	imageID := newUUID()
	key := storage.AICCImageKey(agent.AppID, session.ID, imageID, filename)
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
	if err := s.store.UpdateAICCSessionLeadStatus(ctx, sqlc.UpdateAICCSessionLeadStatusParams{
		ID:         session.ID,
		LeadStatus: status,
	}); err != nil {
		return AICCPublicLeadValuesResult{}, fmt.Errorf("更新 AICC 留资状态失败: %w", err)
	}
	return AICCPublicLeadValuesResult{LeadStatus: status, MissingRequiredKeys: aiccLeadFieldKeys(missing)}, nil
}

// SubmitFeedback 写入助手回复反馈，并同步会话解决状态。
func (s *AICCPublicService) SubmitFeedback(ctx context.Context, input AICCPublicFeedbackInput) (AICCPublicFeedbackResult, error) {
	if s.tx != nil {
		var result AICCPublicFeedbackResult
		err := s.tx.WithAICCPublicTx(ctx, func(store AICCPublicStore) error {
			next := *s
			next.store = store
			var runErr error
			result, runErr = next.submitFeedback(ctx, input)
			return runErr
		})
		return result, err
	}
	return s.submitFeedback(ctx, input)
}

func (s *AICCPublicService) submitFeedback(ctx context.Context, input AICCPublicFeedbackInput) (AICCPublicFeedbackResult, error) {
	messageID := strings.TrimSpace(input.MessageID)
	sessionToken := strings.TrimSpace(input.SessionToken)
	if messageID == "" || sessionToken == "" {
		return AICCPublicFeedbackResult{}, ErrAICCInvalidMessage
	}
	message, err := s.store.GetAICCAssistantMessageForFeedback(ctx, sqlc.GetAICCAssistantMessageForFeedbackParams{
		ID:           messageID,
		SessionToken: sessionToken,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return AICCPublicFeedbackResult{}, ErrAICCInvalidMessage
	}
	if err != nil {
		return AICCPublicFeedbackResult{}, fmt.Errorf("查询 AICC 反馈消息失败: %w", err)
	}
	if _, err := s.activeAgentByMessage(ctx, message); err != nil {
		return AICCPublicFeedbackResult{}, err
	}
	if err := s.store.UpsertAICCFeedback(ctx, sqlc.UpsertAICCFeedbackParams{
		ID:        newUUID(),
		SessionID: message.SessionID,
		MessageID: message.ID,
		Helpful:   input.Helpful,
	}); err != nil {
		return AICCPublicFeedbackResult{}, fmt.Errorf("保存 AICC 回复反馈失败: %w", err)
	}
	status := domain.AICCResolutionUnresolved
	if input.Helpful {
		status = domain.AICCResolutionResolved
	}
	if err := s.store.UpdateAICCSessionResolutionStatus(ctx, sqlc.UpdateAICCSessionResolutionStatusParams{
		ID:               message.SessionID,
		ResolutionStatus: status,
	}); err != nil {
		return AICCPublicFeedbackResult{}, fmt.Errorf("更新 AICC 会话解决状态失败: %w", err)
	}
	return AICCPublicFeedbackResult{ResolutionStatus: status}, nil
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
