package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	null "github.com/guregu/null/v5"

	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/channel"
	"oc-manager/internal/integrations/ocops"
	redactlog "oc-manager/internal/log"
	"oc-manager/internal/store/sqlc"
)

// ChannelEndpointResolver 把 appID 解析为 oc-ops 调用坐标（基址 + per-app token）。
// 微信扫码登录走 oc-ops HTTP SSE，BeginAuth 时需要把目标 app 实例的 Endpoint 注入
// channel.AuthInput，runner 据此把登录请求路由到正确的 oc-ops 实例。
// 生产实现为 *service.OcOpsResolverFromStore；为避免引入 service 包依赖、保持 worker
// 可独立单测，这里只声明返回 ocops.Endpoint 的窄接口，由装配侧用闭包适配。
type ChannelEndpointResolver interface {
	ResolveEndpoint(ctx context.Context, appID string) (ocops.Endpoint, error)
}

// ChannelLoginStore 是渠道登录 worker 需要的最小存储接口。
type ChannelLoginStore interface {
	GetApp(ctx context.Context, id string) (sqlc.App, error)
	GetChannelBindingByAppAndType(ctx context.Context, arg sqlc.GetChannelBindingByAppAndTypeParams) (sqlc.ChannelBinding, error)
	SetChannelBindingChallenge(ctx context.Context, arg sqlc.SetChannelBindingChallengeParams) error
	SetChannelBindingStatus(ctx context.Context, arg sqlc.SetChannelBindingStatusParams) error
	MarkChannelBindingBound(ctx context.Context, arg sqlc.MarkChannelBindingBoundParams) error
	SetAppStatus(ctx context.Context, arg sqlc.SetAppStatusParams) error
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) error
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) error
}

// ChannelStartLoginHandler 执行 channel_start_login job。
type ChannelStartLoginHandler struct {
	store    ChannelLoginStore
	registry *channel.Registry
	// endpoints 把 appID 解析为 oc-ops 坐标，供 BeginAuth 填充 AuthInput.Endpoint。
	// nil 时降级为零值 Endpoint（向后兼容旧 docker exec 装配 / 单测无需 oc-ops 寻址的场景）。
	endpoints ChannelEndpointResolver
}

// NewChannelStartLoginHandler 创建 channel_start_login handler。
// resolver 用于把目标 app 解析为 oc-ops 调用坐标；传 nil 时 Endpoint 留零值。
func NewChannelStartLoginHandler(store ChannelLoginStore, registry *channel.Registry, resolver ChannelEndpointResolver) *ChannelStartLoginHandler {
	return &ChannelStartLoginHandler{store: store, registry: registry, endpoints: resolver}
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
	// 解析目标 app 的 oc-ops 坐标，注入 AuthInput.Endpoint：微信扫码登录走 oc-ops SSE，
	// runner 据此路由到正确实例。解析失败仅告警不阻断（Endpoint 留零值，由下游 BeginAuth
	// 在不可达时报错），避免 resolver 抖动直接吞掉登录请求。
	var endpoint ocops.Endpoint
	if h.endpoints != nil {
		if ep, rerr := h.endpoints.ResolveEndpoint(ctx, payload.AppID); rerr != nil {
			slog.WarnContext(ctx, "解析 oc-ops 坐标失败，Endpoint 留空", "app_id", payload.AppID, "error", rerr)
		} else {
			endpoint = ep
		}
	}
	challenge, err := adapter.BeginAuth(ctx, channel.AuthInput{
		AppID:       payload.AppID,
		OwnerUserID: app.OwnerUserID,
		Endpoint:    endpoint,
	})
	if err != nil {
		safeMessage := redactlog.SafeErrorMessage(err)
		_ = h.store.SetChannelBindingStatus(ctx, sqlc.SetChannelBindingStatusParams{
			AppID:       binding.AppID,
			ChannelType: binding.ChannelType,
			Status:      domain.ChannelStatusFailed,
			LastError:   null.StringFrom(safeMessage),
		})
		if auditErr := recordChannelAppAudit(ctx, h.store, app, "channel_auth_start", "failed", safeMessage,
			fmt.Sprintf("渠道 %s", channelLabelWorker(payload.ChannelType)),
			map[string]any{
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
	if err := h.store.SetChannelBindingChallenge(ctx, sqlc.SetChannelBindingChallengeParams{
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
	app, err := h.store.GetApp(ctx, payload.AppID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
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

// ChannelRestarter 抽象重启 app 运行时（hermes）让其重载渠道 platform 配置的能力。
// k8s 下由 Orchestrator.RolloutRestart 实现（按 appID 重建 pod）。
type ChannelRestarter interface {
	RestartApp(ctx context.Context, appID string) error
}

// ChannelCheckBindingHandler 执行 channel_check_binding job。
type ChannelCheckBindingHandler struct {
	store     ChannelLoginStore
	registry  *channel.Registry
	resolver  channel.BindingResolver
	restarter ChannelRestarter
}

// NewChannelCheckBindingHandler 创建 channel_check_binding handler。
func NewChannelCheckBindingHandler(store ChannelLoginStore, registry *channel.Registry, resolver channel.BindingResolver) *ChannelCheckBindingHandler {
	return &ChannelCheckBindingHandler{store: store, registry: registry, resolver: resolver}
}

// SetRestarter 注入容器重启能力,bound 后触发 hermes 容器重启以重新读 platforms 配置。
func (h *ChannelCheckBindingHandler) SetRestarter(r ChannelRestarter) {
	h.restarter = r
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
		OwnerUserID: app.OwnerUserID,
	})
	if err != nil {
		return fmt.Errorf("查询渠道绑定状态失败: %w", err)
	}
	switch progress.Status {
	case channel.AuthStatusBound:
		identity := progress.BoundIdentity
		if identity == "" && h.resolver != nil && payload.ChannelType == domain.ChannelTypeWeChat {
			if resolved, rerr := h.resolver.ResolveWeChatBoundIdentity(ctx, payload.AppID); rerr == nil {
				identity = resolved
			}
		}
		metadata, _ := json.Marshal(progress.Metadata)
		if err := h.finalizeChannelBound(ctx, app, binding, payload, identity, progress.ChannelName, metadata); err != nil {
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
		_ = h.store.SetChannelBindingStatus(ctx, sqlc.SetChannelBindingStatusParams{
			AppID:       binding.AppID,
			ChannelType: binding.ChannelType,
			Status:      status,
			LastError:   null.StringFrom(safeMessage),
		})
		if err := recordChannelAppAudit(ctx, h.store, app, "channel_bound", "failed", safeMessage,
			fmt.Sprintf("渠道 %s", channelLabelWorker(payload.ChannelType)),
			map[string]any{
				"channel_type": payload.ChannelType,
				"auth_status":  string(progress.Status),
			}); err != nil {
			return err
		}
	default:
		// Fallback：weixin plugin 在 cached login（同微信账号已授权过）场景下
		// 不再 emit "bound" 事件，但 oc-ops ChannelStatus 仍能反映已绑定的真实状态。
		// 这里直接调 resolver 查 oc-ops 是否已有有效身份；
		// 有就同样推到 bound，避免 5 分钟后被错误地 expire。
		if h.resolver != nil && payload.ChannelType == domain.ChannelTypeWeChat {
			if identity, rerr := h.resolver.ResolveWeChatBoundIdentity(ctx, payload.AppID); rerr == nil && identity != "" {
				metadata, _ := json.Marshal(progress.Metadata)
				// 与 case AuthStatusBound 共用 finalizeChannelBound:cached-login
				// fallback 同样要触发容器重启,否则绑定后 weixin 平台不会被 hermes 加载。
				if err := h.finalizeChannelBound(ctx, app, binding, payload, identity, progress.ChannelName, metadata); err != nil {
					return err
				}
				return nil
			}
		}
		_ = h.store.SetChannelBindingStatus(ctx, sqlc.SetChannelBindingStatusParams{
			AppID:       binding.AppID,
			ChannelType: binding.ChannelType,
			Status:      domain.ChannelStatusPendingAuth,
			LastError:   null.String{},
		})
		if err := enqueueChannelCheck(ctx, h.store, payload, 5*time.Second); err != nil {
			return err
		}
	}
	return nil
}

// finalizeChannelBound 把「渠道绑定成功」的统一收尾动作集中到一处:
//  1. MarkChannelBindingBound 写 channel_bindings.status=bound;
//  2. 触发 hermes 容器重启,让其重新读 platforms 配置加载新绑定账号
//     (微信凭证由容器内 oc-channel-login 落盘到 /opt/data/weixin/accounts/,
//     hermes gateway 只在启动期扫描该目录决定启用哪些 messaging platform);
//  3. app 从 binding_waiting 推进到 running;
//  4. 写 channel_bound:succeeded 审计日志。
//
// case AuthStatusBound 与 default 的 cached-login fallback 共用此函数:
// 两条路径都代表"绑定成功",必须走完全相同的收尾,否则任一路径漏掉重启会
// 导致绑定后 weixin 平台不被 hermes 加载(线上表现为"扫码成功但收不到消息")。
//
// 重启失败仅告警不阻塞:主流程已 MarkChannelBindingBound,后续手动重启或
// health check 自愈仍能让账号生效。
func (h *ChannelCheckBindingHandler) finalizeChannelBound(
	ctx context.Context,
	app sqlc.App,
	binding sqlc.ChannelBinding,
	payload channelLoginPayload,
	identity, channelName string,
	metadata []byte,
) error {
	if err := h.store.MarkChannelBindingBound(ctx, sqlc.MarkChannelBindingBoundParams{
		AppID:         binding.AppID,
		ChannelType:   binding.ChannelType,
		BoundIdentity: null.NewString(identity, identity != ""),
		ChannelName:   null.NewString(channelName, channelName != ""),
		MetadataJson:  metadata,
	}); err != nil {
		return fmt.Errorf("标记渠道绑定成功失败: %w", err)
	}
	if h.restarter != nil && payload.ChannelType == domain.ChannelTypeWeChat {
		if err := h.restarter.RestartApp(ctx, app.ID); err != nil {
			slog.ErrorContext(ctx, "渠道绑定后重启 hermes 容器失败", "app_id", app.ID, "error", err)
		}
	}
	if app.Status == domain.AppStatusBindingWaiting {
		if err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusRunning}); err != nil {
			return fmt.Errorf("推进应用状态到 running 失败: %w", err)
		}
	}
	return recordChannelAppAudit(ctx, h.store, app, "channel_bound", "succeeded", "",
		fmt.Sprintf("渠道 %s，身份 %s", channelLabelWorker(payload.ChannelType), identity),
		map[string]any{
			"channel_type":   payload.ChannelType,
			"bound_identity": identity,
			"channel_name":   channelName,
		})
}

func recordChannelAppAudit(ctx context.Context, store ChannelLoginStore, app sqlc.App, action, result, errorMessage, detailMessage string, metadata map[string]any) error {
	raw, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("序列化渠道审计元数据失败: %w", err)
	}
	params := sqlc.CreateAuditLogParams{
		ID:           uuid.NewString(),
		ActorRole:    "system",
		OrgID:        null.StringFrom(app.OrgID),
		TargetType:   "app",
		TargetID:     app.ID,
		Action:       action,
		Result:       result,
		MetadataJson: raw,
	}
	if errorMessage != "" {
		params.ErrorMessage = null.StringFrom(errorMessage)
	}
	if detailMessage != "" {
		params.DetailMessage = null.StringFrom(detailMessage)
	}
	if err := store.CreateAuditLog(ctx, params); err != nil {
		slog.ErrorContext(ctx, "写渠道应用审计失败", "app_id", app.ID, "action", action, "error", err)
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
	if err := store.CreateJob(ctx, sqlc.CreateJobParams{
		ID:          uuid.NewString(),
		Type:        domain.JobTypeChannelCheckBinding,
		Priority:    80,
		RunAfter:    time.Now().Add(delay),
		MaxAttempts: 20,
		PayloadJson: raw,
	}); err != nil {
		return fmt.Errorf("创建 channel_check_binding 任务失败: %w", err)
	}
	return nil
}

// channelLabelWorker 是 worker 包内的渠道枚举到中文映射，与 service.channelLabel 同义。
// worker 不依赖 service 包，因而在此独立维护一份；新增渠道时两份同步更新。
func channelLabelWorker(channelType string) string {
	switch channelType {
	case domain.ChannelTypeWeChat:
		return "微信"
	default:
		return channelType
	}
}

// textOrEmpty 从 null.String 取值，nil/无效时返回空串。
// channel_login handler 内的旧调用点通过此函数保持语义。
func textOrEmpty(s null.String) string {
	return s.ValueOrZero()
}
