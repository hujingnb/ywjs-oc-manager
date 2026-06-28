package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/channel"
	redactlog "oc-manager/internal/log"
	"oc-manager/internal/store/sqlc"
)

// FeishuSecretPatcher 抽象给 app 的 control-token Secret 增删飞书 key 的能力，
// 由 k8sorch.KubernetesAdapter.PatchSecretKeys 实现。解绑时删 feishu-* key
// 使引擎下次重启不再装载飞书凭证。
type FeishuSecretPatcher interface {
	PatchSecretKeys(ctx context.Context, appID string, set map[string]string, del []string) error
}

// ChannelRestarter 抽象重启 app 运行时（hermes）让其重载渠道 platform 配置的能力。
// k8s 下由 Orchestrator.RolloutRestart 实现（按 appID 重建 pod）。
type ChannelRestarter interface {
	RestartApp(ctx context.Context, appID string) error
}

// ChannelStore 抽象渠道服务的数据访问能力。
type ChannelStore interface {
	GetApp(ctx context.Context, id string) (sqlc.App, error)
	GetChannelBindingByAppAndType(ctx context.Context, arg sqlc.GetChannelBindingByAppAndTypeParams) (sqlc.ChannelBinding, error)
	SetChannelBindingStatus(ctx context.Context, arg sqlc.SetChannelBindingStatusParams) error
	// UpsertChannelBindingUnbound 为飞书 create-on-demand 建绑定行（已存在则 no-op）。
	UpsertChannelBindingUnbound(ctx context.Context, arg sqlc.UpsertChannelBindingUnboundParams) error
	// SetFeishuCredentials 写入飞书凭证 metadata 并置状态（扫码发起阶段仅 domain 占位）。
	SetFeishuCredentials(ctx context.Context, arg sqlc.SetFeishuCredentialsParams) error
	// SetChannelBindingChallenge 按 (app_id, channel_type) 置 pending_auth + 写 metadata + 清 last_error。
	// 企业微信手填发起用它落库已加密的 secret metadata。
	SetChannelBindingChallenge(ctx context.Context, arg sqlc.SetChannelBindingChallengeParams) error
	// SetAppStatus 裸 UPDATE app.status，无状态机守卫；守卫由调用方在 Go 层负责。
	SetAppStatus(ctx context.Context, arg sqlc.SetAppStatusParams) error
	// SetAppRuntimePhase 写运行时就绪维度;解绑 RolloutRestart 前置 restarting(业务态 status 不动)。
	SetAppRuntimePhase(ctx context.Context, arg sqlc.SetAppRuntimePhaseParams) error
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) error
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) error
}

// ChannelService 协调 channel adapter 与 channel_bindings 表。
type ChannelService struct {
	store    ChannelStore
	registry *channel.Registry
	notifier JobNotifier
	// feishuPatcher / feishuRestarter 用于飞书解绑即时清理：删 app Secret 的 feishu-* key
	// 并重启 pod，使引擎下次重启不再启用飞书平台。微信解绑不依赖二者。
	// 经 SetFeishuUnbindDeps 注入；k8s 未启用时留 nil，解绑仅置 DB 状态不报错。
	// 现同时服务飞书与企业微信解绑/绑定注入（PatchSecretKeys/RestartApp 渠道无关）。
	feishuPatcher   FeishuSecretPatcher
	feishuRestarter ChannelRestarter
	// cipher 用于企业微信手填发起时加密 secret 落 metadata（飞书加密在 worker check，企业微信在 service）。
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

// SetFeishuUnbindDeps 注入飞书解绑所需的 Secret patch 与重启能力。
// 与构造分离：k8s 编排器经类型断言取得（PatchSecretKeys 仅 *KubernetesAdapter 暴露），
// 未启用 k8s 时不注入，解绑飞书分支因 patcher==nil 跳过。
func (s *ChannelService) SetFeishuUnbindDeps(p FeishuSecretPatcher, r ChannelRestarter) {
	s.feishuPatcher, s.feishuRestarter = p, r
}

// SetCipher 注入加密器，供企业微信手填发起时加密 secret 落库。未注入时 BeginWorkWechatAuth 报错。
func (s *ChannelService) SetCipher(c *auth.Cipher) { s.cipher = c }

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
	// 实例就绪守卫：pod 不在服务（restarting 重启窗口 / 版本升级 init 子状态 / stopped 等）时
	// 发起会打到不可达的 oc-ops 拿到 502，提前返回友好错误，不写库不入队。
	if !domain.AppCanInitiateChannelAuth(app.Status, app.RuntimePhase) {
		return ChallengeResult{}, ErrInstanceNotReady
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
		ID:           newUUID(),
		ActorID:      null.StringFrom(principal.UserID),
		ActorRole:    principal.Role,
		OrgID:        null.StringFrom(app.OrgID),
		TargetType:   "app",
		TargetID:     app.ID,
		Action:       "channel_auth_start",
		Result:       "succeeded",
		MetadataJson: auditMetadata,
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
	// Domain 是飞书域：feishu | lark，空值回退 feishu。
	Domain string
}

// BeginFeishuAuth 是飞书扫码自动创建发起入口（与微信 BeginAuth 并列，handler 按渠道分流）。
// 飞书无预建绑定行，先 create-on-demand；仅写 domain 占位（凭证由 worker 经 adapter 扫码取得），
// 入队 channel_start_login job 让 worker 起 oc-ops SSE 出二维码并推进后续阶段。
func (s *ChannelService) BeginFeishuAuth(ctx context.Context, principal auth.Principal, appID string, in FeishuAuthInput) (ChallengeResult, error) {
	app, err := s.loadManageableApp(ctx, principal, appID)
	if err != nil {
		return ChallengeResult{}, err
	}
	// 实例就绪守卫（与微信 BeginAuth 同口径）：pod 不在服务时不发起，返回友好错误，
	// 不 create-on-demand、不写 metadata、不入队，覆盖解绑重启 + 版本升级两个窗口。
	if !domain.AppCanInitiateChannelAuth(app.Status, app.RuntimePhase) {
		return ChallengeResult{}, ErrInstanceNotReady
	}
	if s.registry == nil {
		return ChallengeResult{}, ErrChannelAdapterMissing
	}
	if _, err := s.registry.Lookup(domain.ChannelTypeFeishu); err != nil {
		return ChallengeResult{}, fmt.Errorf("%w: %s", ErrChannelAdapterMissing, domain.ChannelTypeFeishu)
	}
	// bound 短路（对齐微信 BeginAuth）：已绑定的飞书 app 再次发起直接返回 bound，
	// 不重跑 upsert / 写 metadata / 入队 job。飞书 binding 首次发起时尚不存在
	// （create-on-demand），ErrNoRows 属正常路径，继续往下走 upsert。
	existing, err := s.store.GetChannelBindingByAppAndType(ctx, sqlc.GetChannelBindingByAppAndTypeParams{AppID: app.ID, ChannelType: domain.ChannelTypeFeishu})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ChallengeResult{}, fmt.Errorf("查询飞书绑定失败: %w", err)
	}
	if err == nil && existing.Status == domain.ChannelStatusBound {
		return ChallengeResult{Status: domain.ChannelStatusBound, ChannelType: domain.ChannelTypeFeishu}, nil
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
	// 扫码自动创建：此刻尚无凭证，仅暂存 domain，worker 经 adapter 取二维码/凭证。
	meta := map[string]any{
		"domain":      feishuDomain,
		"acquired_by": "qr",
		"injected":    "false",
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
	// 入队 channel_start_login：payload 带 domain，worker 起 oc-ops SSE 出二维码扫码注册。
	payload, err := json.Marshal(map[string]any{
		"app_id":       app.ID,
		"channel_type": domain.ChannelTypeFeishu,
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

// WorkWechatAuthInput 是企业微信手填发起的 service 入参（与 handler 的 WorkWechatChannelAuthRequest 对应）。
type WorkWechatAuthInput struct {
	// BotID 是企业微信智能机器人 Bot ID（明文）。
	BotID string
	// Secret 是机器人 Secret 明文（service 内加密后落 metadata，明文注入 k8s Secret）。
	Secret string
}

// BeginWorkWechatAuth 是企业微信手填发起入口（与微信 BeginAuth / 飞书 BeginFeishuAuth 并列，handler 按渠道分流）。
// 企业微信无扫码：凭证随请求体同步到达，故此处一次性完成「加密落库 + 同步注入 Secret + 重启 + 置 restarting + 入队连通探测」。
// 注入/重启复用飞书解绑同款 patcher/restarter（PatchSecretKeys/RestartApp 渠道无关）。
func (s *ChannelService) BeginWorkWechatAuth(ctx context.Context, principal auth.Principal, appID string, in WorkWechatAuthInput) (ChallengeResult, error) {
	app, err := s.loadManageableApp(ctx, principal, appID)
	if err != nil {
		return ChallengeResult{}, err
	}
	// 实例就绪守卫（与微信 / 飞书发起同口径）：pod 不在服务时不发起，返回友好错误，
	// 不加密、不写库、不注入、不入队，覆盖解绑重启 + 版本升级两个窗口。
	if !domain.AppCanInitiateChannelAuth(app.Status, app.RuntimePhase) {
		return ChallengeResult{}, ErrInstanceNotReady
	}
	if s.registry == nil {
		return ChallengeResult{}, ErrChannelAdapterMissing
	}
	if _, err := s.registry.Lookup(domain.ChannelTypeWorkWeChat); err != nil {
		return ChallengeResult{}, fmt.Errorf("%w: %s", ErrChannelAdapterMissing, domain.ChannelTypeWorkWeChat)
	}
	// cipher 必需：企业微信 secret 必须加密后落 metadata；未注入直接报错而非明文落库。
	if s.cipher == nil {
		return ChallengeResult{}, fmt.Errorf("企业微信发起缺少 cipher，无法加密 secret")
	}
	// bound 短路（对齐飞书）：已绑定的企业微信 app 再次发起直接返回 bound，不重跑 upsert / 写 metadata / 注入 / 入队。
	// 首次发起时绑定行可能尚不存在（create-on-demand），ErrNoRows 属正常路径，继续往下走。
	existing, err := s.store.GetChannelBindingByAppAndType(ctx, sqlc.GetChannelBindingByAppAndTypeParams{AppID: app.ID, ChannelType: domain.ChannelTypeWorkWeChat})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ChallengeResult{}, fmt.Errorf("查询企业微信绑定失败: %w", err)
	}
	if err == nil && existing.Status == domain.ChannelStatusBound {
		return ChallengeResult{Status: domain.ChannelStatusBound, ChannelType: domain.ChannelTypeWorkWeChat}, nil
	}
	// create-on-demand：企业微信绑定行不在实例创建时预建，发起时按需建立（已存在 no-op）。
	if err := s.store.UpsertChannelBindingUnbound(ctx, sqlc.UpsertChannelBindingUnboundParams{
		ID:          newUUID(),
		AppID:       app.ID,
		ChannelType: domain.ChannelTypeWorkWeChat,
	}); err != nil {
		return ChallengeResult{}, fmt.Errorf("创建企业微信绑定行失败: %w", err)
	}
	// 加密 secret：Encrypt 返回 string 密文，与 buildAppSpec 解密读取 secret_ciphertext 喂 Cipher.Decrypt 的格式一致。
	enc, err := s.cipher.Encrypt([]byte(in.Secret))
	if err != nil {
		return ChallengeResult{}, fmt.Errorf("加密企业微信 secret 失败: %w", err)
	}
	metaJSON, err := json.Marshal(map[string]any{
		"bot_id":            in.BotID,
		"secret_ciphertext": enc,
	})
	if err != nil {
		return ChallengeResult{}, fmt.Errorf("序列化企业微信 metadata 失败: %w", err)
	}
	if err := s.store.SetChannelBindingChallenge(ctx, sqlc.SetChannelBindingChallengeParams{
		MetadataJson: metaJSON,
		AppID:        app.ID,
		ChannelType:  domain.ChannelTypeWorkWeChat,
	}); err != nil {
		return ChallengeResult{}, fmt.Errorf("写入企业微信凭证失败: %w", err)
	}
	// 同步注入 k8s Secret：明文 bot_id/secret 写入 app Secret，引擎重启后装载企业微信平台。
	if s.feishuPatcher != nil {
		if err := s.feishuPatcher.PatchSecretKeys(ctx, app.ID, map[string]string{
			"wecom-bot-id": in.BotID,
			"wecom-secret": in.Secret,
		}, nil); err != nil {
			return ChallengeResult{}, fmt.Errorf("注入企业微信 Secret 失败: %w", err)
		}
	}
	// 解绑触发 RolloutRestart 重建 pod(Recreate,~20s 停机),期间 oc-ops 不可用。
	// 双轴模型:置 runtime_phase=restarting 标记运行时不就绪(发起闸门据此关闭),业务态 status
	// 保持不动;reconciler 在 pod 重新 Ready 后写回 ready。置位失败只记日志、不阻断解绑——
	// channel_binding=unbound_by_user 才是 source of truth。
	if err := s.store.SetAppRuntimePhase(ctx, sqlc.SetAppRuntimePhaseParams{
		RuntimePhase: domain.RuntimePhaseRestarting,
		ID:           app.ID,
	}); err != nil {
		slog.ErrorContext(ctx, "解绑置 runtime_phase=restarting 失败", "app_id", app.ID, redactlog.Err(err))
	}
	if s.feishuRestarter != nil {
		if err := s.feishuRestarter.RestartApp(ctx, app.ID); err != nil {
			slog.ErrorContext(ctx, "企业微信注入后重启失败", "app_id", app.ID, redactlog.Err(err))
		}
	}
	// 入队 channel_check_binding：worker 经 oc-ops 探测 platforms.wecom 连通态并写回绑定结果。
	payload, err := json.Marshal(map[string]any{
		"app_id":       app.ID,
		"channel_type": domain.ChannelTypeWorkWeChat,
		"requested_by": principal.UserID,
	})
	if err != nil {
		return ChallengeResult{}, fmt.Errorf("序列化企业微信探测任务失败: %w", err)
	}
	jobID := newUUID()
	if err := s.store.CreateJob(ctx, sqlc.CreateJobParams{
		ID:          jobID,
		Type:        domain.JobTypeChannelCheckBinding,
		Priority:    80,
		RunAfter:    time.Now().Add(5 * time.Second),
		MaxAttempts: 20,
		PayloadJson: payload,
	}); err != nil {
		return ChallengeResult{}, fmt.Errorf("创建企业微信探测任务失败: %w", err)
	}
	if s.notifier != nil {
		_ = s.notifier.Enqueue(ctx, jobID)
	}
	return ChallengeResult{Status: domain.ChannelStatusPendingAuth, ChannelType: domain.ChannelTypeWorkWeChat, JobID: jobID}, nil
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
	// 飞书 / 企业微信解绑是用户即时动作（不走 worker）：删 app Secret 对应 key 并重启，
	// 使引擎下次重启不再启用该平台。删 key / 重启失败只记日志不阻断——
	// DB status=unbound_by_user 已是 source of truth，凭证残留也不会被引擎装载。
	if delKeys := unbindSecretKeys(channelType); delKeys != nil && s.feishuPatcher != nil {
		if err := s.feishuPatcher.PatchSecretKeys(ctx, app.ID, nil, delKeys); err != nil {
			slog.ErrorContext(ctx, "解绑删渠道 Secret key 失败", "app_id", app.ID, "channel", channelType, redactlog.Err(err))
		}
		// 解绑触发 RolloutRestart 重建 pod(Recreate,~20s 停机),期间 oc-ops 不可用。
		// 双轴模型:置 runtime_phase=restarting 标记运行时不就绪(发起闸门据此关闭),业务态 status
		// 保持不动;reconciler 在 pod 重新 Ready 后写回 ready。置位失败只记日志、不阻断解绑——
		// channel_binding=unbound_by_user 才是 source of truth。
		if err := s.store.SetAppRuntimePhase(ctx, sqlc.SetAppRuntimePhaseParams{
			RuntimePhase: domain.RuntimePhaseRestarting,
			ID:           app.ID,
		}); err != nil {
			slog.ErrorContext(ctx, "解绑置 runtime_phase=restarting 失败", "app_id", app.ID, redactlog.Err(err))
		}
		if s.feishuRestarter != nil {
			if err := s.feishuRestarter.RestartApp(ctx, app.ID); err != nil {
				slog.ErrorContext(ctx, "解绑后重启失败", "app_id", app.ID, redactlog.Err(err))
			}
		}
	}
	return nil
}

// unbindSecretKeys 返回某渠道解绑时需从 app Secret 删除的 key 列表。
// 飞书和企业微信属于 env 注入型渠道：绑定时写入 k8s Secret，解绑时同步删除，
// 使引擎下次重启不再装载对应平台凭证。非 env 注入型渠道（如微信文件态）返回 nil。
func unbindSecretKeys(channelType string) []string {
	switch channelType {
	case domain.ChannelTypeFeishu:
		return []string{"feishu-app-id", "feishu-app-secret", "feishu-domain"}
	case domain.ChannelTypeWorkWeChat:
		return []string{"wecom-bot-id", "wecom-secret"}
	default:
		return nil
	}
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
