package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/channel"
	"oc-manager/internal/integrations/hermes"
	redactlog "oc-manager/internal/log"
	"oc-manager/internal/store/sqlc"
)

// ChannelLoginStore 是渠道登录 worker 需要的最小存储接口。
type ChannelLoginStore interface {
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	GetChannelBindingByAppAndType(ctx context.Context, arg sqlc.GetChannelBindingByAppAndTypeParams) (sqlc.ChannelBinding, error)
	SetChannelBindingChallenge(ctx context.Context, arg sqlc.SetChannelBindingChallengeParams) (sqlc.ChannelBinding, error)
	SetChannelBindingStatus(ctx context.Context, arg sqlc.SetChannelBindingStatusParams) (sqlc.ChannelBinding, error)
	MarkChannelBindingBound(ctx context.Context, arg sqlc.MarkChannelBindingBoundParams) (sqlc.ChannelBinding, error)
	SetAppStatus(ctx context.Context, arg sqlc.SetAppStatusParams) (sqlc.App, error)
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error)
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error)
}

// ChannelStartLoginHandler 执行 channel_start_login job。
type ChannelStartLoginHandler struct {
	store    ChannelLoginStore
	registry *channel.Registry
}

// NewChannelStartLoginHandler 创建 channel_start_login handler。
func NewChannelStartLoginHandler(store ChannelLoginStore, registry *channel.Registry) *ChannelStartLoginHandler {
	return &ChannelStartLoginHandler{store: store, registry: registry}
}

// Handle 在容器内触发渠道登录，保存二维码 challenge，并排队轮询绑定状态。
func (h *ChannelStartLoginHandler) Handle(ctx context.Context, job sqlc.Job) error {
	if job.Type != domain.JobTypeChannelStartLogin {
		return fmt.Errorf("非 channel_start_login 任务: %s", job.Type)
	}
	payload, err := decodeChannelLoginPayload(job.PayloadJson)
	if err != nil {
		return err
	}
	app, binding, adapter, err := h.load(ctx, payload)
	if err != nil {
		return err
	}
	if binding.Status == domain.ChannelStatusBound {
		return nil
	}
	challenge, err := adapter.BeginAuth(ctx, channel.AuthInput{
		AppID:       payload.AppID,
		OwnerUserID: uuidToString(app.OwnerUserID),
		NodeID:      uuidToString(app.RuntimeNodeID),
		ContainerID: textOrEmpty(app.ContainerID),
	})
	if err != nil {
		safeMessage := redactlog.SafeErrorMessage(err)
		_, _ = h.store.SetChannelBindingStatus(ctx, sqlc.SetChannelBindingStatusParams{
			AppID:       binding.AppID,
			ChannelType: binding.ChannelType,
			Status:      domain.ChannelStatusFailed,
			LastError:   pgtype.Text{String: safeMessage, Valid: safeMessage != ""},
		})
		if auditErr := recordChannelAppAudit(ctx, h.store, app, "channel_auth_start", "failed", safeMessage, map[string]any{
			"channel_type": payload.ChannelType,
		}); auditErr != nil {
			return auditErr
		}
		return fmt.Errorf("发起渠道登录失败: %w", err)
	}
	metadata, err := json.Marshal(map[string]any{
		"type":       challenge.Type,
		"qrcode":     challenge.QRCode,
		"code":       challenge.Code,
		"expires_at": challenge.ExpiresAt,
		"hints":      challenge.Hints,
	})
	if err != nil {
		return fmt.Errorf("序列化渠道挑战失败: %w", err)
	}
	if _, err := h.store.SetChannelBindingChallenge(ctx, sqlc.SetChannelBindingChallengeParams{
		AppID:        binding.AppID,
		ChannelType:  binding.ChannelType,
		MetadataJson: metadata,
	}); err != nil {
		return fmt.Errorf("保存渠道挑战失败: %w", err)
	}
	if err := h.enqueueCheck(ctx, payload, 5*time.Second); err != nil {
		return err
	}
	return nil
}

func (h *ChannelStartLoginHandler) load(ctx context.Context, payload channelLoginPayload) (sqlc.App, sqlc.ChannelBinding, channel.ChannelAdapter, error) {
	if h.registry == nil {
		return sqlc.App{}, sqlc.ChannelBinding{}, nil, fmt.Errorf("%w: %s", channel.ErrAdapterNotFound, payload.ChannelType)
	}
	adapter, err := h.registry.Lookup(payload.ChannelType)
	if err != nil {
		return sqlc.App{}, sqlc.ChannelBinding{}, nil, err
	}
	appID, err := parseUUID(payload.AppID)
	if err != nil {
		return sqlc.App{}, sqlc.ChannelBinding{}, nil, fmt.Errorf("非法 app_id: %w", err)
	}
	app, err := h.store.GetApp(ctx, appID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return sqlc.App{}, sqlc.ChannelBinding{}, nil, fmt.Errorf("应用不存在: %s", payload.AppID)
		}
		return sqlc.App{}, sqlc.ChannelBinding{}, nil, fmt.Errorf("查询应用失败: %w", err)
	}
	binding, err := h.store.GetChannelBindingByAppAndType(ctx, sqlc.GetChannelBindingByAppAndTypeParams{
		AppID:       app.ID,
		ChannelType: payload.ChannelType,
	})
	if err != nil {
		return sqlc.App{}, sqlc.ChannelBinding{}, nil, fmt.Errorf("查询渠道绑定失败: %w", err)
	}
	return app, binding, adapter, nil
}

func (h *ChannelStartLoginHandler) enqueueCheck(ctx context.Context, payload channelLoginPayload, delay time.Duration) error {
	return enqueueChannelCheck(ctx, h.store, payload, delay)
}

// ChannelRestarter 抽象重启 hermes 容器的能力(让 Hermes 加载新 .env 中的 weixin 凭证)。
type ChannelRestarter interface {
	RestartContainer(ctx context.Context, nodeID, containerID string) error
}

// ChannelCheckBindingHandler 执行 channel_check_binding job。
type ChannelCheckBindingHandler struct {
	store        ChannelLoginStore
	registry     *channel.Registry
	resolver     channel.BindingResolver
	runtimeFiles AppRuntimeFileWriter
	restarter    ChannelRestarter
	newAPIURL    string
	// cipher 用于解密 app.NewapiKeyCiphertext,取真实 OPENAI_API_KEY 写入 .env。
	// nil 时 bound 流程跳过 OPENAI_API_KEY 解密(降级:仅写 WEIXIN_*)。
	cipher *auth.Cipher
}

// NewChannelCheckBindingHandler 创建 channel_check_binding handler。
func NewChannelCheckBindingHandler(store ChannelLoginStore, registry *channel.Registry, resolver channel.BindingResolver) *ChannelCheckBindingHandler {
	return &ChannelCheckBindingHandler{store: store, registry: registry, resolver: resolver}
}

// SetRuntimeFileWriter 注入 .env 上传能力,bound 时把 WEIXIN_* 追加写入容器 .env。
func (h *ChannelCheckBindingHandler) SetRuntimeFileWriter(w AppRuntimeFileWriter) {
	h.runtimeFiles = w
}

// SetRestarter 注入容器重启能力,写完 .env 后重启 hermes 容器加载 weixin platform。
func (h *ChannelCheckBindingHandler) SetRestarter(r ChannelRestarter) {
	h.restarter = r
}

// SetNewAPIBaseURL 注入 new-api 内网 URL,用于渲染新 .env 中的 OPENAI_BASE_URL。
func (h *ChannelCheckBindingHandler) SetNewAPIBaseURL(url string) {
	h.newAPIURL = url
}

// SetCipher 注入 Cipher,bound 时用于解密 app.NewapiKeyCiphertext 取真实 OPENAI_API_KEY。
// 必须与 AppInitializeConfig.Cipher 使用同一 Cipher 实例，确保解密密钥一致。
func (h *ChannelCheckBindingHandler) SetCipher(c *auth.Cipher) {
	h.cipher = c
}

// Handle 查询渠道绑定状态，bound 后补写身份并把 binding_waiting 应用推进到 running。
func (h *ChannelCheckBindingHandler) Handle(ctx context.Context, job sqlc.Job) error {
	if job.Type != domain.JobTypeChannelCheckBinding {
		return fmt.Errorf("非 channel_check_binding 任务: %s", job.Type)
	}
	payload, err := decodeChannelLoginPayload(job.PayloadJson)
	if err != nil {
		return err
	}
	app, binding, adapter, err := h.load(ctx, payload)
	if err != nil {
		return err
	}
	if binding.Status == domain.ChannelStatusBound {
		return nil
	}
	progress, err := adapter.PollAuth(ctx, channel.AuthInput{
		AppID:       payload.AppID,
		OwnerUserID: uuidToString(app.OwnerUserID),
		NodeID:      uuidToString(app.RuntimeNodeID),
		ContainerID: textOrEmpty(app.ContainerID),
	})
	if err != nil {
		return fmt.Errorf("查询渠道绑定状态失败: %w", err)
	}
	switch progress.Status {
	case channel.AuthStatusBound:
		identity := progress.BoundIdentity
		if identity == "" && h.resolver != nil && payload.ChannelType == domain.ChannelTypeWeChat {
			if resolved, rerr := h.resolver.ResolveWeChatBoundIdentity(ctx, uuidToString(app.RuntimeNodeID), textOrEmpty(app.ContainerID)); rerr == nil {
				identity = resolved
			}
		}
		metadata, _ := json.Marshal(progress.Metadata)
		if _, err := h.store.MarkChannelBindingBound(ctx, sqlc.MarkChannelBindingBoundParams{
			AppID:         binding.AppID,
			ChannelType:   binding.ChannelType,
			BoundIdentity: pgtype.Text{String: identity, Valid: identity != ""},
			ChannelName:   pgtype.Text{String: progress.ChannelName, Valid: progress.ChannelName != ""},
			MetadataJson:  metadata,
		}); err != nil {
			return fmt.Errorf("标记渠道绑定成功失败: %w", err)
		}
		// 把完整 .env(OPENAI_* + WEIXIN_DM_POLICY + WEIXIN_*)写入容器,再触发重启让 Hermes 加载 weixin platform。
		// progress.Metadata 由 wechat.go consumeStream 在 bound 事件填充:
		//   weixin_account_id / weixin_token / weixin_base_url / weixin_user_id。
		// 必须重写完整 .env 而非仅追加 WEIXIN_*:UploadAppRuntimeFile 是覆盖写,
		// 追加写法会把 OPENAI_* 行丢失,导致 Hermes 重启后调 new-api 返回 HTTP 401。
		if h.runtimeFiles != nil && payload.ChannelType == domain.ChannelTypeWeChat &&
			progress.Metadata != nil && progress.Metadata["weixin_token"] != "" {
			newAPIURL := h.newAPIURL
			if newAPIURL == "" {
				newAPIURL = "http://new-api:3000"
			}
			// 解密拿真实 OPENAI_API_KEY；cipher 未注入或解密失败时降级用空串(不写 OPENAI_*)。
			openAIToken := ""
			if h.cipher != nil && app.NewapiKeyCiphertext.Valid && app.NewapiKeyCiphertext.String != "" {
				if tok, decErr := decryptCiphertext(app.NewapiKeyCiphertext.String, h.cipher); decErr == nil {
					openAIToken = tok
				} else {
					slog.WarnContext(ctx, "bound .env 解密 OPENAI_API_KEY 失败,使用空 token", "app_id", uuidToString(app.ID), "error", decErr)
				}
			}
			fullEnv := hermes.RenderEnv(hermes.EnvInput{
				NewAPIURL:       newAPIURL,
				NewAPIToken:     openAIToken,
				WeixinAccountID: progress.Metadata["weixin_account_id"],
				WeixinToken:     progress.Metadata["weixin_token"],
				WeixinBaseURL:   progress.Metadata["weixin_base_url"],
			})
			if err := h.runtimeFiles.UploadAppRuntimeFile(ctx, uuidToString(app.RuntimeNodeID), uuidToString(app.ID), ".env", strings.NewReader(fullEnv)); err != nil {
				slog.ErrorContext(ctx, "写入完整 .env 失败", "app_id", uuidToString(app.ID), "error", err)
			} else if h.restarter != nil {
				if err := h.restarter.RestartContainer(ctx, uuidToString(app.RuntimeNodeID), textOrEmpty(app.ContainerID)); err != nil {
					slog.ErrorContext(ctx, "重启 hermes 容器失败", "app_id", uuidToString(app.ID), "error", err)
				}
			}
		}
		if app.Status == domain.AppStatusBindingWaiting {
			if _, err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusRunning}); err != nil {
				return fmt.Errorf("推进应用状态到 running 失败: %w", err)
			}
		}
		if err := recordChannelAppAudit(ctx, h.store, app, "channel_bound", "succeeded", "", map[string]any{
			"channel_type":   payload.ChannelType,
			"bound_identity": identity,
			"channel_name":   progress.ChannelName,
		}); err != nil {
			return err
		}
	case channel.AuthStatusFailed, channel.AuthStatusExpired:
		status := domain.ChannelStatusFailed
		if progress.Status == channel.AuthStatusExpired {
			status = domain.ChannelStatusExpired
		}
		safeMessage := string(progress.Status)
		if progress.ErrorMessage != "" {
			safeMessage = redactlog.SafeErrorMessage(errors.New(progress.ErrorMessage))
		}
		_, _ = h.store.SetChannelBindingStatus(ctx, sqlc.SetChannelBindingStatusParams{
			AppID:       binding.AppID,
			ChannelType: binding.ChannelType,
			Status:      status,
			LastError:   pgtype.Text{String: safeMessage, Valid: safeMessage != ""},
		})
		if err := recordChannelAppAudit(ctx, h.store, app, "channel_bound", "failed", safeMessage, map[string]any{
			"channel_type": payload.ChannelType,
			"auth_status":  string(progress.Status),
		}); err != nil {
			return err
		}
	default:
		// Fallback：weixin plugin 在 cached login（同微信账号已授权过）场景下
		// 不再 emit "bound" 事件，但 plugin state 文件（/root/.openclaw/openclaw-weixin/accounts/*.json，
		// legacy 路径，Hermes 容器通过 bind mount 保持相同路径）真实存在 session。
		// 这里直接调 resolver 看 plugin state 是否已有有效身份；
		// 有就同样推到 bound，避免 5 分钟后被错误地 expire。
		if h.resolver != nil && payload.ChannelType == domain.ChannelTypeWeChat {
			if identity, rerr := h.resolver.ResolveWeChatBoundIdentity(ctx, uuidToString(app.RuntimeNodeID), textOrEmpty(app.ContainerID)); rerr == nil && identity != "" {
				metadata, _ := json.Marshal(progress.Metadata)
				if _, err := h.store.MarkChannelBindingBound(ctx, sqlc.MarkChannelBindingBoundParams{
					AppID:         binding.AppID,
					ChannelType:   binding.ChannelType,
					BoundIdentity: pgtype.Text{String: identity, Valid: true},
					ChannelName:   pgtype.Text{String: progress.ChannelName, Valid: progress.ChannelName != ""},
					MetadataJson:  metadata,
				}); err != nil {
					return fmt.Errorf("基于 plugin state 标记渠道绑定成功失败: %w", err)
				}
				if app.Status == domain.AppStatusBindingWaiting {
					if _, err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusRunning}); err != nil {
						return fmt.Errorf("推进应用状态到 running 失败: %w", err)
					}
				}
				if err := recordChannelAppAudit(ctx, h.store, app, "channel_bound", "succeeded", "", map[string]any{
					"channel_type":   payload.ChannelType,
					"bound_identity": identity,
					"channel_name":   progress.ChannelName,
				}); err != nil {
					return err
				}
				return nil
			}
		}
		_, _ = h.store.SetChannelBindingStatus(ctx, sqlc.SetChannelBindingStatusParams{
			AppID:       binding.AppID,
			ChannelType: binding.ChannelType,
			Status:      domain.ChannelStatusPendingAuth,
			LastError:   pgtype.Text{},
		})
		if err := enqueueChannelCheck(ctx, h.store, payload, 5*time.Second); err != nil {
			return err
		}
	}
	return nil
}

func recordChannelAppAudit(ctx context.Context, store ChannelLoginStore, app sqlc.App, action, result, errorMessage string, metadata map[string]any) error {
	raw, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("序列化渠道审计元数据失败: %w", err)
	}
	params := sqlc.CreateAuditLogParams{
		ActorRole:    "system",
		OrgID:        app.OrgID,
		TargetType:   "app",
		TargetID:     uuidToString(app.ID),
		Action:       action,
		Result:       result,
		MetadataJson: raw,
	}
	if errorMessage != "" {
		params.ErrorMessage = pgtype.Text{String: errorMessage, Valid: true}
	}
	if _, err := store.CreateAuditLog(ctx, params); err != nil {
		slog.ErrorContext(ctx, "写渠道应用审计失败", "app_id", uuidToString(app.ID), "action", action, "error", err)
		return fmt.Errorf("写入渠道应用审计日志失败: %w", err)
	}
	return nil
}

func (h *ChannelCheckBindingHandler) load(ctx context.Context, payload channelLoginPayload) (sqlc.App, sqlc.ChannelBinding, channel.ChannelAdapter, error) {
	start := ChannelStartLoginHandler{store: h.store, registry: h.registry}
	return start.load(ctx, payload)
}

type channelLoginPayload struct {
	AppID       string `json:"app_id"`
	ChannelType string `json:"channel_type"`
}

func decodeChannelLoginPayload(raw []byte) (channelLoginPayload, error) {
	var payload channelLoginPayload
	if len(raw) == 0 {
		return payload, fmt.Errorf("payload 为空")
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return payload, fmt.Errorf("解析 payload 失败: %w", err)
	}
	if payload.AppID == "" {
		return payload, fmt.Errorf("payload 缺少 app_id")
	}
	if payload.ChannelType == "" {
		payload.ChannelType = domain.ChannelTypeWeChat
	}
	return payload, nil
}

func enqueueChannelCheck(ctx context.Context, store ChannelLoginStore, payload channelLoginPayload, delay time.Duration) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化 channel_check_binding payload 失败: %w", err)
	}
	if _, err := store.CreateJob(ctx, sqlc.CreateJobParams{
		Type:        domain.JobTypeChannelCheckBinding,
		Priority:    80,
		RunAfter:    pgtype.Timestamptz{Time: time.Now().Add(delay), Valid: true},
		MaxAttempts: 20,
		PayloadJson: raw,
	}); err != nil {
		return fmt.Errorf("创建 channel_check_binding 任务失败: %w", err)
	}
	return nil
}
