package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/channel"
	"oc-manager/internal/store/sqlc"
)

// ChannelStore 抽象渠道服务的数据访问能力。
type ChannelStore interface {
	GetApp(ctx context.Context, id string) (sqlc.App, error)
	GetChannelBindingByAppAndType(ctx context.Context, arg sqlc.GetChannelBindingByAppAndTypeParams) (sqlc.ChannelBinding, error)
	SetChannelBindingStatus(ctx context.Context, arg sqlc.SetChannelBindingStatusParams) error
	// UpsertChannelBindingUnbound 为飞书 create-on-demand 建绑定行（已存在则 no-op）。
	UpsertChannelBindingUnbound(ctx context.Context, arg sqlc.UpsertChannelBindingUnboundParams) error
	// SetFeishuCredentials 写入飞书凭证 metadata 并置状态（手填阶段含 secret 密文）。
	SetFeishuCredentials(ctx context.Context, arg sqlc.SetFeishuCredentialsParams) error
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) error
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) error
}

// ChannelService 协调 channel adapter 与 channel_bindings 表。
type ChannelService struct {
	store    ChannelStore
	registry *channel.Registry
	notifier JobNotifier
	// cipher 用于加密飞书手填的 app_secret 后入库；扫码模式与微信渠道不依赖它。
	// 经 SetCipher 注入，复用 manager 进程内同一份 master_key 的 Cipher 实例。
	cipher *auth.Cipher
}

// NewChannelService 创建 service。
func NewChannelService(store ChannelStore, registry *channel.Registry, notifier ...JobNotifier) *ChannelService {
	var n JobNotifier
	if len(notifier) > 0 {
		n = notifier[0]
	}
	return &ChannelService{store: store, registry: registry, notifier: n}
}

// SetCipher 注入加密原语。飞书手填模式需要它加密 app_secret；
// 与构造分离是为了避免给已用 variadic notifier 的构造再塞一个可选参数，保持调用点清晰。
func (s *ChannelService) SetCipher(cipher *auth.Cipher) {
	s.cipher = cipher
}

// ChallengeResult 是 BeginAuth 对外返回的视图。
type ChallengeResult struct {
	// Status 是渠道绑定状态，pending_auth 表示后台 job 正在发起登录挑战。
	Status string `json:"status"`
	// ChannelType 是渠道标识，例如 wechat。
	ChannelType string `json:"channel_type"`
	// ChallengeType 是登录挑战类型，例如 qrcode 或 code；异步 worker 未生成时为空。
	ChallengeType string `json:"challenge_type,omitempty"`
	// QRCode 是二维码内容或 URL，具体格式由 channel adapter 决定。
	QRCode string `json:"qrcode,omitempty"`
	// Code 是非二维码登录场景的一次性验证码。
	Code string `json:"code,omitempty"`
	// ExpiresAt 是挑战过期时间；零值表示当前响应没有同步挑战。
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	// Hints 是 adapter 返回的展示提示，key/value 均为前端可直接展示的安全文本。
	Hints map[string]string `json:"hints,omitempty"`
	// JobID 是异步 channel_start_login job ID，前端可据此追踪后台任务。
	JobID string `json:"job_id,omitempty"`
}

// ProgressResult 是 PollAuth 对外返回的视图。
type ProgressResult struct {
	// Status 是当前渠道绑定状态，直接来自 channel_bindings.status。
	Status string `json:"status"`
	// BoundIdentity 是渠道侧已绑定身份，如微信号或 OpenID 的展示值。
	BoundIdentity string `json:"bound_identity,omitempty"`
	// ChannelName 是渠道侧账号或会话名称。
	ChannelName string `json:"channel_name,omitempty"`
	// ErrorMessage 是最近一次绑定失败原因，已由 worker 写入安全错误文本。
	ErrorMessage string `json:"error_message,omitempty"`
	// UpdatedAt 是绑定记录最近更新时间，用于前端判断轮询新鲜度。
	UpdatedAt time.Time `json:"updated_at"`
	// Metadata 是绑定过程产生的附加展示信息，会经过 channelBindingMetadata 归一化。
	Metadata map[string]string `json:"metadata,omitempty"`
}

// BeginAuth 启动指定应用、指定渠道的登录挑战。
// HTTP 层不直接执行 runtime 容器命令：真实登录由 channel_start_login worker 完成。
// 这里只负责权限校验、渠道可用性校验、状态置为 pending_auth 并入队任务，
// 避免微信插件加载或二维码生成阻塞请求线程。
func (s *ChannelService) BeginAuth(ctx context.Context, principal auth.Principal, appID, channelType string) (ChallengeResult, error) {
	app, err := s.loadManageableApp(ctx, principal, appID)
	if err != nil {
		return ChallengeResult{}, err
	}
	if s.registry == nil {
		return ChallengeResult{}, ErrChannelAdapterMissing
	}
	if _, err := s.registry.Lookup(channelType); err != nil {
		return ChallengeResult{}, fmt.Errorf("%w: %s", ErrChannelAdapterMissing, channelType)
	}
	binding, err := s.store.GetChannelBindingByAppAndType(ctx, sqlc.GetChannelBindingByAppAndTypeParams{AppID: app.ID, ChannelType: channelType})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ChallengeResult{}, ErrNotFound
		}
		return ChallengeResult{}, fmt.Errorf("查询渠道绑定失败: %w", err)
	}
	if binding.Status == domain.ChannelStatusBound {
		return ChallengeResult{Status: domain.ChannelStatusBound, ChannelType: channelType}, nil
	}
	// SetChannelBindingStatus 为 :exec；LastError 清空写 null.String{}。
	if err := s.store.SetChannelBindingStatus(ctx, sqlc.SetChannelBindingStatusParams{
		AppID:       binding.AppID,
		ChannelType: binding.ChannelType,
		Status:      domain.ChannelStatusPendingAuth,
		LastError:   null.String{},
	}); err != nil {
		return ChallengeResult{}, fmt.Errorf("更新渠道状态失败: %w", err)
	}
	payload, err := json.Marshal(map[string]any{
		"app_id":       app.ID,
		"channel_type": channelType,
		"requested_by": principal.UserID,
	})
	if err != nil {
		return ChallengeResult{}, fmt.Errorf("序列化渠道登录任务失败: %w", err)
	}
	// CreateJob 为 :exec；预先生成 job ID 以便后续 notifier 入队和审计元数据记录。
	jobID := newUUID()
	if err := s.store.CreateJob(ctx, sqlc.CreateJobParams{
		ID:          jobID,
		Type:        domain.JobTypeChannelStartLogin,
		Priority:    90,
		RunAfter:    time.Now(),
		MaxAttempts: 3,
		PayloadJson: payload,
	}); err != nil {
		return ChallengeResult{}, fmt.Errorf("创建渠道登录任务失败: %w", err)
	}
	auditMetadata, err := json.Marshal(map[string]any{
		"channel_type": channelType,
		"job_id":       jobID,
		"requested_by": principal.UserID,
	})
	if err != nil {
		return ChallengeResult{}, fmt.Errorf("序列化渠道发起审计元数据失败: %w", err)
	}
	// ActorID / OrgID 由字符串直接转 null.String。
	if err := s.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
		ID:            newUUID(),
		ActorID:       null.StringFrom(principal.UserID),
		ActorRole:     principal.Role,
		OrgID:         null.StringFrom(app.OrgID),
		TargetType:    "app",
		TargetID:      app.ID,
		Action:        "channel_auth_start",
		Result:        "succeeded",
		MetadataJson:  auditMetadata,
		// DetailMessage 已迁移到 metadata.channel_type，此处不再写入冻结中文文案。
	}); err != nil {
		return ChallengeResult{}, fmt.Errorf("写入渠道发起审计日志失败: %w", err)
	}
	if s.notifier != nil {
		_ = s.notifier.Enqueue(ctx, jobID)
	}
	return ChallengeResult{
		Status:      domain.ChannelStatusPendingAuth,
		ChannelType: channelType,
		JobID:       jobID,
	}, nil
}

// FeishuAuthInput 是飞书发起的 service 入参（与 handler 的 FeishuChannelAuthRequest 对应）。
type FeishuAuthInput struct {
	// Mode 是发起模式：scan 扫码自动创建、manual 手填兜底。
	Mode string
	// Domain 是飞书域：feishu | lark，空值回退 feishu。
	Domain string
	// AppID 是飞书自建应用 App ID，manual 模式必填。
	AppID string
	// AppSecret 是飞书自建应用 App Secret，manual 模式必填，加密后写入 metadata。
	AppSecret string
}

// BeginFeishuAuth 是飞书双模式发起入口（与微信 BeginAuth 并列，handler 按渠道分流）。
// 飞书无预建绑定行，先 create-on-demand；manual 模式加密 secret 后写凭证 metadata，
// scan 模式仅写 domain 占位（凭证由 worker 经 adapter 取得）；两模式都入队
// channel_start_login job，worker 按 payload.mode 区分推进阶段。
func (s *ChannelService) BeginFeishuAuth(ctx context.Context, principal auth.Principal, appID string, in FeishuAuthInput) (ChallengeResult, error) {
	app, err := s.loadManageableApp(ctx, principal, appID)
	if err != nil {
		return ChallengeResult{}, err
	}
	if s.registry == nil {
		return ChallengeResult{}, ErrChannelAdapterMissing
	}
	if _, err := s.registry.Lookup(domain.ChannelTypeFeishu); err != nil {
		return ChallengeResult{}, fmt.Errorf("%w: %s", ErrChannelAdapterMissing, domain.ChannelTypeFeishu)
	}
	// create-on-demand：飞书绑定行不在实例创建时预建，发起时按需建立（已存在 no-op）。
	if err := s.store.UpsertChannelBindingUnbound(ctx, sqlc.UpsertChannelBindingUnboundParams{
		ID:          newUUID(),
		AppID:       app.ID,
		ChannelType: domain.ChannelTypeFeishu,
	}); err != nil {
		return ChallengeResult{}, fmt.Errorf("创建飞书绑定行失败: %w", err)
	}
	feishuDomain := in.Domain
	if feishuDomain == "" {
		feishuDomain = "feishu"
	}
	var meta map[string]any
	if in.Mode == "manual" {
		// 手填兜底：app_id/app_secret 必填，secret 加密入库，明文绝不落 metadata。
		if in.AppID == "" || in.AppSecret == "" {
			return ChallengeResult{}, ErrInvalidChannelCredential
		}
		if s.cipher == nil {
			return ChallengeResult{}, fmt.Errorf("飞书 secret 加密器未配置")
		}
		enc, err := s.cipher.Encrypt([]byte(in.AppSecret))
		if err != nil {
			return ChallengeResult{}, fmt.Errorf("加密飞书 secret 失败: %w", err)
		}
		meta = map[string]any{
			"app_id":                in.AppID,
			"app_secret_ciphertext": enc,
			"domain":                feishuDomain,
			"acquired_by":           "manual",
			"injected":              "false",
		}
	} else {
		// 扫码自动创建：此刻尚无凭证，仅暂存 domain，worker 经 adapter 取二维码/凭证。
		meta = map[string]any{
			"domain":      feishuDomain,
			"acquired_by": "qr",
			"injected":    "false",
		}
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return ChallengeResult{}, fmt.Errorf("序列化飞书 metadata 失败: %w", err)
	}
	if err := s.store.SetFeishuCredentials(ctx, sqlc.SetFeishuCredentialsParams{
		MetadataJson: metaJSON,
		Status:       domain.ChannelStatusPendingAuth,
		AppID:        app.ID,
	}); err != nil {
		return ChallengeResult{}, fmt.Errorf("写入飞书凭证失败: %w", err)
	}
	// 入队 channel_start_login：payload 带 mode/domain，worker 据此分流扫码或手填注册。
	payload, err := json.Marshal(map[string]any{
		"app_id":       app.ID,
		"channel_type": domain.ChannelTypeFeishu,
		"mode":         in.Mode,
		"domain":       feishuDomain,
		"requested_by": principal.UserID,
	})
	if err != nil {
		return ChallengeResult{}, fmt.Errorf("序列化飞书登录任务失败: %w", err)
	}
	jobID := newUUID()
	if err := s.store.CreateJob(ctx, sqlc.CreateJobParams{
		ID:          jobID,
		Type:        domain.JobTypeChannelStartLogin,
		Priority:    90,
		RunAfter:    time.Now(),
		MaxAttempts: 3,
		PayloadJson: payload,
	}); err != nil {
		return ChallengeResult{}, fmt.Errorf("创建飞书登录任务失败: %w", err)
	}
	if s.notifier != nil {
		_ = s.notifier.Enqueue(ctx, jobID)
	}
	return ChallengeResult{
		Status:      domain.ChannelStatusPendingAuth,
		ChannelType: domain.ChannelTypeFeishu,
		JobID:       jobID,
	}, nil
}

// PollAuth 查询登录进度。真实状态推进由 channel_check_binding worker 完成；
// 这里只读取 DB 中的 channel_bindings，保证轮询接口轻量且可恢复。
func (s *ChannelService) PollAuth(ctx context.Context, principal auth.Principal, appID, channelType string) (ProgressResult, error) {
	app, err := s.loadViewableApp(ctx, principal, appID)
	if err != nil {
		return ProgressResult{}, err
	}
	binding, err := s.store.GetChannelBindingByAppAndType(ctx, sqlc.GetChannelBindingByAppAndTypeParams{AppID: app.ID, ChannelType: channelType})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProgressResult{}, ErrNotFound
		}
		return ProgressResult{}, fmt.Errorf("查询渠道绑定失败: %w", err)
	}
	metadata := map[string]string{}
	if len(binding.MetadataJson) > 0 {
		metadata = channelBindingMetadata(binding.MetadataJson)
	}
	// UpdatedAt 是 time.Time（MySQL DATETIME），直接使用。
	updatedAt := time.Now()
	if binding.UpdatedAt != (time.Time{}) {
		updatedAt = binding.UpdatedAt
	}
	errorMessage := ""
	if binding.LastError.Valid {
		errorMessage = binding.LastError.String
	}
	boundIdentity := ""
	if binding.BoundIdentity.Valid {
		boundIdentity = binding.BoundIdentity.String
	}
	channelName := ""
	if binding.ChannelName.Valid {
		channelName = binding.ChannelName.String
	}
	return ProgressResult{
		Status:        binding.Status,
		BoundIdentity: boundIdentity,
		ChannelName:   channelName,
		ErrorMessage:  errorMessage,
		UpdatedAt:     updatedAt,
		Metadata:      metadata,
	}, nil
}

// Unbind 解绑指定渠道，状态置为 unbound_by_user。
func (s *ChannelService) Unbind(ctx context.Context, principal auth.Principal, appID, channelType string) error {
	app, err := s.loadManageableApp(ctx, principal, appID)
	if err != nil {
		return err
	}
	binding, err := s.store.GetChannelBindingByAppAndType(ctx, sqlc.GetChannelBindingByAppAndTypeParams{AppID: app.ID, ChannelType: channelType})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("查询渠道绑定失败: %w", err)
	}
	if err := s.store.SetChannelBindingStatus(ctx, sqlc.SetChannelBindingStatusParams{
		AppID:       binding.AppID,
		ChannelType: binding.ChannelType,
		Status:      domain.ChannelStatusUnboundByUser,
		LastError:   null.String{},
	}); err != nil {
		return fmt.Errorf("解绑渠道失败: %w", err)
	}
	return nil
}

// loadViewableApp 校验主体是否可读取应用渠道进度。
// 渠道轮询属于只读视图，平台管理员保留跨组织观察能力。
func (s *ChannelService) loadViewableApp(ctx context.Context, principal auth.Principal, appID string) (sqlc.App, error) {
	app, err := s.store.GetApp(ctx, appID)
	if errors.Is(err, sql.ErrNoRows) {
		return sqlc.App{}, ErrNotFound
	}
	if err != nil {
		return sqlc.App{}, fmt.Errorf("查询应用失败: %w", err)
	}
	if !auth.CanViewApp(principal, app.OrgID, app.OwnerUserID) {
		return sqlc.App{}, ErrForbidden
	}
	return app, nil
}

// loadManageableApp 校验主体是否可修改应用渠道绑定。
// BeginAuth / Unbind 都会写 channel_bindings，因此平台管理员不可越权执行。
func (s *ChannelService) loadManageableApp(ctx context.Context, principal auth.Principal, appID string) (sqlc.App, error) {
	app, err := s.loadViewableApp(ctx, principal, appID)
	if err != nil {
		return sqlc.App{}, err
	}
	if !auth.CanManageApp(principal, app.OrgID, app.OwnerUserID) {
		return sqlc.App{}, ErrForbidden
	}
	return app, nil
}

// channelBindingMetadata 将 channel_bindings.metadata_json 归一化为 string map。
// worker 可能写入嵌套 hints，本函数只保留字符串值，避免 handler 泄露复杂内部结构。
func channelBindingMetadata(raw []byte) map[string]string {
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return map[string]string{}
	}
	metadata := make(map[string]string, len(data))
	for key, value := range data {
		// 过滤密文 / secret 类敏感字段，不得透传前端（如飞书 app_secret_ciphertext）。
		if strings.Contains(key, "ciphertext") || strings.Contains(strings.ToLower(key), "secret") {
			continue
		}
		switch v := value.(type) {
		case string:
			metadata[key] = v
		case map[string]any:
			for hintKey, hintValue := range v {
				if hint, ok := hintValue.(string); ok {
					metadata[hintKey] = hint
				}
			}
		}
	}
	return metadata
}
