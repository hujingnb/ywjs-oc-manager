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

	"oc-manager/internal/auth"
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
	SetFeishuCredentials(ctx context.Context, arg sqlc.SetFeishuCredentialsParams) error
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
	// worker 执行窗口守卫：HTTP service 入队时实例可能还是 ready，但 job 真正执行前
	// 可能已因解绑、升级或 k8s 重建进入 restarting。这里必须再次检查，否则会打到
	// 暂不可用的 oc-ops，把临时 502 误写成渠道绑定失败。
	if !domain.AppCanInitiateChannelAuth(app.Status, app.RuntimePhase) {
		if appStatusCanRetryChannelStart(app.Status) {
			if err := h.store.SetChannelBindingStatus(ctx, sqlc.SetChannelBindingStatusParams{
				AppID:       binding.AppID,
				ChannelType: binding.ChannelType,
				Status:      domain.ChannelStatusPendingAuth,
				LastError:   null.String{},
			}); err != nil {
				return fmt.Errorf("暂缓渠道登录时更新渠道状态失败: %w", err)
			}
			return enqueueChannelStart(ctx, h.store, payload, 5*time.Second)
		}
		return nil
	}
	// 解析目标 app 的 oc-ops 坐标，注入 AuthInput.Endpoint：微信扫码登录走 oc-ops SSE，
	// runner 据此路由到正确实例。解析失败仅告警不阻断（Endpoint 留零值，由下游 BeginAuth
	// 在不可达时报错），避免 resolver 抖动直接吞掉登录请求。
	var endpoint ocops.Endpoint
	if h.endpoints != nil {
		if ep, rerr := h.endpoints.ResolveEndpoint(ctx, payload.AppID); rerr != nil {
			slog.WarnContext(ctx, "解析 oc-ops 坐标失败，Endpoint 留空", "app_id", payload.AppID, redactlog.Err(rerr))
		} else {
			endpoint = ep
		}
	}
	// 飞书分支：与微信「引擎自落盘」模式不同，走扫码自动创建。
	// 起 oc-ops SSE 取二维码，domain（feishu|lark）经 AuthInput.ChannelName 透传，
	// BeginAuth 拿到首个 qrcode 事件即返回，后台 goroutine 继续等扫码授权回填凭证。
	if payload.ChannelType == domain.ChannelTypeFeishu {
		challenge, err := adapter.BeginAuth(ctx, channel.AuthInput{
			AppID:       payload.AppID,
			OwnerUserID: app.OwnerUserID,
			ChannelName: payload.Domain,
			Endpoint:    endpoint,
		})
		if err != nil {
			_ = h.store.SetChannelBindingStatus(ctx, sqlc.SetChannelBindingStatusParams{
				AppID:       binding.AppID,
				ChannelType: binding.ChannelType,
				Status:      domain.ChannelStatusFailed,
				LastError:   null.StringFrom(channelStartErrorMessage(err)),
			})
			return fmt.Errorf("发起飞书扫码失败: %w", err)
		}
		meta, err := json.Marshal(map[string]any{
			"type":        challenge.Type,
			"qrcode":      challenge.QRCode,
			"expires_at":  challenge.ExpiresAt,
			"acquired_by": "qr",
			"domain":      payload.Domain,
		})
		if err != nil {
			return fmt.Errorf("序列化飞书二维码失败: %w", err)
		}
		if err := h.store.SetChannelBindingChallenge(ctx, sqlc.SetChannelBindingChallengeParams{
			AppID:        binding.AppID,
			ChannelType:  binding.ChannelType,
			MetadataJson: meta,
		}); err != nil {
			return fmt.Errorf("保存飞书二维码失败: %w", err)
		}
		return h.enqueueCheck(ctx, payload, 5*time.Second)
	}
	challenge, err := adapter.BeginAuth(ctx, channel.AuthInput{
		AppID:       payload.AppID,
		OwnerUserID: app.OwnerUserID,
		Endpoint:    endpoint,
	})
	if err != nil {
		safeMessage := channelStartErrorMessage(err)
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

// FeishuSecretPatcher 抽象「给 app 的 control-token Secret 增删飞书 key」的能力，
// 由 k8sorch.KubernetesAdapter.PatchSecretKeys 实现。飞书凭证以 feishu-app-id /
// feishu-app-secret / feishu-domain 注入 Secret，引擎重启后从 env 读取明文连接开放平台。
type FeishuSecretPatcher interface {
	PatchSecretKeys(ctx context.Context, appID string, set map[string]string, del []string) error
}

// FeishuHealthClient 抽象「查飞书在开放平台侧的连通态」，由 oc-ops ChannelStatus(feishu) 适配。
// 返回 state（platform_state，如 connected/fatal）、botOpenID（health 不回传，恒为空，
// 由 worker 改用 metadata 的 bot_open_id）、errMessage（异常原因）。
type FeishuHealthClient interface {
	FeishuStatus(ctx context.Context, appID string) (state, botOpenID, errMessage string, err error)
}

// ChannelCheckBindingHandler 执行 channel_check_binding job。
type ChannelCheckBindingHandler struct {
	store     ChannelLoginStore
	registry  *channel.Registry
	resolver  channel.BindingResolver
	restarter ChannelRestarter
	// feishuPatcher / cipher / feishuHealth 仅飞书两阶段 check 使用：
	// feishuPatcher 把扫码凭证注入 app Secret，cipher 把 secret 加密后落 metadata，
	// feishuHealth 在注入重启后探测开放平台连通态。微信路径不依赖三者。
	feishuPatcher FeishuSecretPatcher
	cipher        *auth.Cipher
	feishuHealth  FeishuHealthClient
}

// NewChannelCheckBindingHandler 创建 channel_check_binding handler。
func NewChannelCheckBindingHandler(store ChannelLoginStore, registry *channel.Registry, resolver channel.BindingResolver) *ChannelCheckBindingHandler {
	return &ChannelCheckBindingHandler{store: store, registry: registry, resolver: resolver}
}

// SetRestarter 注入容器重启能力,bound 后触发 hermes 容器重启以重新读 platforms 配置。
func (h *ChannelCheckBindingHandler) SetRestarter(r ChannelRestarter) {
	h.restarter = r
}

// SetFeishuDeps 注入飞书两阶段 check 所需依赖：Secret patch、密文 cipher、health 探测客户端。
// 仅飞书渠道走这三者；未注入时飞书 check 降级（patcher 为 nil 跳过注入、health 为 nil 重新入队）。
func (h *ChannelCheckBindingHandler) SetFeishuDeps(p FeishuSecretPatcher, cipher *auth.Cipher, hc FeishuHealthClient) {
	h.feishuPatcher, h.cipher, h.feishuHealth = p, cipher, hc
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
	// 飞书走专用两阶段流程（注入凭证 → health 探测连通），与微信「引擎自落盘 + PollAuth 判 bound」
	// 模式完全不同，故在此分流；微信及其余渠道继续走下面的 PollAuth 逻辑。
	if payload.ChannelType == domain.ChannelTypeFeishu {
		return h.handleFeishuCheck(ctx, app, binding, payload, adapter)
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
		// 连通探测超时终态：对设了 deadline 的渠道（钉钉——引擎不上报 fatal，错误凭证只会一直
		// 连不上，default→Pending 是唯一归宿，否则永久 pending + 每 5s 无限 re-enqueue）。
		// 到点仍未 connected 即置 failed、写统一超时文案、停止 re-enqueue。未设 deadline 的渠道
		// （微信/企业微信，CheckDeadlineUnix==0）行为完全不变。
		if payload.CheckDeadlineUnix > 0 && time.Now().Unix() > payload.CheckDeadlineUnix {
			timeoutMsg := channelCheckTimeoutMessage(payload.ChannelType)
			_ = h.store.SetChannelBindingStatus(ctx, sqlc.SetChannelBindingStatusParams{
				AppID:       binding.AppID,
				ChannelType: binding.ChannelType,
				Status:      domain.ChannelStatusFailed,
				LastError:   null.StringFrom(timeoutMsg),
			})
			if err := recordChannelAppAudit(ctx, h.store, app, "channel_bound", "failed", timeoutMsg,
				fmt.Sprintf("渠道 %s", channelLabelWorker(payload.ChannelType)),
				map[string]any{
					"channel_type": payload.ChannelType,
					"auth_status":  "timeout",
				}); err != nil {
				return err
			}
			return nil
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

// channelCheckTimeoutMessage 返回连通探测超时的用户可读文案（写入 binding.last_error，前端展示）。
// 钉钉 / 企业微信给出针对性自查引导；其余渠道用通用文案。
func channelCheckTimeoutMessage(channelType string) string {
	switch channelType {
	case domain.ChannelTypeDingTalk:
		return "连接超时，请检查 Client ID / Client Secret 是否正确、机器人是否已在钉钉开放平台启用 Stream 推送模式"
	case domain.ChannelTypeWorkWeChat:
		return "连接超时，请检查 Bot ID / Secret 是否正确、智能机器人是否已在企业微信后台配置为 API 长连接模式"
	default:
		return "连接超时，请检查渠道凭证是否正确、机器人是否已在对应平台正确配置"
	}
}

// handleFeishuCheck 执行飞书两阶段 check，靠 metadata 的 injected 标记区分阶段：
//
//	阶段1（injected != "true"）：取凭证（扫码经 adapter.TakeCredentials；重试时从 metadata
//	  解密已写入的密文）→【幂等四步】① 先把凭证密文写 metadata 但 injected 仍 "false"（持久化优先，
//	  使凭证不丢、扫码内存凭证落库可恢复）→ ② PatchSecretKeys 注入 feishu-* 到 app Secret
//	  （失败即 return error 触发重试，injected 仍 false）→ ③ RolloutRestart 重建 pod 让引擎读 env
//	  → ④ 注入成功后才翻 injected="true" → 入队 check 进入阶段2。凭证未就绪时：adapter 已 failed
//	  则置 failed，否则继续等（re-enqueue）。
//	阶段2（injected == "true"）：经 oc-ops health 查 platform_state →
//	  connected 则 MarkChannelBindingBound（identity 取 metadata 的 bot_open_id，
//	  channel_name 取 metadata 的 bot_name——health 不回传 bot_open_id）；
//	  fatal 则置 failed 带原因；其余态 re-enqueue 继续等连接建立。
func (h *ChannelCheckBindingHandler) handleFeishuCheck(ctx context.Context, app sqlc.App, binding sqlc.ChannelBinding, payload channelLoginPayload, adapter channel.ChannelAdapter) error {
	var meta map[string]any
	_ = json.Unmarshal(binding.MetadataJson, &meta)
	injected, _ := meta["injected"].(string)

	if injected != "true" {
		// ── 阶段1：取凭证 → 加密落库 → 注入 Secret → 重启 ──
		creds, ok := h.takeFeishuCredentials(adapter, payload, meta)
		if !ok {
			// 凭证尚未就绪：扫码授权回填需时间。adapter 若已 failed（如用户拒绝 / 注册失败）
			// 立即置 failed；否则继续等下一轮。
			if p, perr := adapter.PollAuth(ctx, channel.AuthInput{AppID: payload.AppID}); perr == nil && p.Status == channel.AuthStatusFailed {
				return h.failFeishu(ctx, app, binding, payload, p.ErrorMessage)
			}
			return enqueueChannelCheck(ctx, h.store, payload, 3*time.Second)
		}
		// 扫码凭证已在 credentials 事件回填 bot_name/bot_open_id，直接用于落库；
		// 偶发为空时即为空（引擎建连后阶段2 health 仍能驱动 bound）。
		if h.cipher == nil {
			return fmt.Errorf("飞书 check 缺少 cipher，无法加密 secret")
		}
		enc, err := h.cipher.Encrypt([]byte(creds.AppSecret))
		if err != nil {
			return fmt.Errorf("加密飞书 secret 失败: %w", err)
		}
		// acquired_by 沿用原 metadata（扫码路径恒为 qr）；缺失默认 qr。
		acquiredBy, _ := meta["acquired_by"].(string)
		if acquiredBy == "" {
			acquiredBy = "qr"
		}
		// 凭证全集 metadata；injected 字段在持久化/翻转两个阶段分别置 false/true，
		// 其余字段两次写入完全一致，保证幂等：重试时凭证已在 DB、可重新注入。
		credMeta := map[string]any{
			"app_id":                creds.AppID,
			"app_secret_ciphertext": enc,
			"domain":                creds.Domain,
			"acquired_by":           acquiredBy,
			"bot_name":              creds.BotName,
			"bot_open_id":           creds.BotOpenID,
		}
		// ① 持久化优先：先把凭证密文 + bot 信息落库，但 injected 仍 "false"。
		// 这一步把扫码内存凭证固化到 DB，且使后续 patch/重启失败可重试（重试时
		// injected 仍 false → 重新进阶段1 → takeFeishuCredentials 从 DB 密文恢复）。
		credMeta["injected"] = "false"
		persistMeta, err := json.Marshal(credMeta)
		if err != nil {
			return fmt.Errorf("序列化飞书凭证 metadata 失败: %w", err)
		}
		if err := h.store.SetFeishuCredentials(ctx, sqlc.SetFeishuCredentialsParams{
			MetadataJson: persistMeta,
			Status:       domain.ChannelStatusPendingAuth,
			AppID:        app.ID,
		}); err != nil {
			return fmt.Errorf("写入飞书凭证失败: %w", err)
		}
		// ② 注入 app Secret：引擎重启后从 env 读 feishu-* 明文连接开放平台。
		// 失败必须 return error（injected 仍 false）触发重试，否则飞书 key 永不注入。
		// PatchSecretKeys 幂等：重试 set 相同 key 无副作用。
		if h.feishuPatcher != nil {
			if err := h.feishuPatcher.PatchSecretKeys(ctx, app.ID, map[string]string{
				"feishu-app-id":     creds.AppID,
				"feishu-app-secret": creds.AppSecret,
				"feishu-domain":     creds.Domain,
			}, nil); err != nil {
				return fmt.Errorf("patch 飞书 Secret 失败: %w", err)
			}
		}
		// ③ 重启失败仅告警不阻塞：metadata/Secret 已就绪，后续阶段2 health 探测仍能驱动 bound。
		if h.restarter != nil {
			if err := h.restarter.RestartApp(ctx, app.ID); err != nil {
				slog.ErrorContext(ctx, "飞书凭证注入后重启 hermes 失败", "app_id", app.ID, redactlog.Err(err))
			}
		}
		// ④ 注入 + 重启完成后才翻 injected="true"，使下一轮 check 进入阶段2 health 探测。
		// 其余字段重复写一遍保持幂等。
		credMeta["injected"] = "true"
		injectedMeta, err := json.Marshal(credMeta)
		if err != nil {
			return fmt.Errorf("序列化飞书凭证 metadata 失败: %w", err)
		}
		if err := h.store.SetFeishuCredentials(ctx, sqlc.SetFeishuCredentialsParams{
			MetadataJson: injectedMeta,
			Status:       domain.ChannelStatusPendingAuth,
			AppID:        app.ID,
		}); err != nil {
			return fmt.Errorf("翻转飞书 injected 标记失败: %w", err)
		}
		// 等重启 + 引擎建立长连接，再进入阶段2 health 探测。
		return enqueueChannelCheck(ctx, h.store, payload, 8*time.Second)
	}

	// ── 阶段2：health 探测开放平台连通态 ──
	if h.feishuHealth == nil {
		return enqueueChannelCheck(ctx, h.store, payload, 5*time.Second)
	}
	state, _, errMsg, err := h.feishuHealth.FeishuStatus(ctx, app.ID)
	if err != nil {
		// health 查询本身失败（实例尚在重启 / 暂不可达）：继续等，不直接判失败。
		return enqueueChannelCheck(ctx, h.store, payload, 5*time.Second)
	}
	switch state {
	case "connected":
		// identity / channel_name 取 metadata（health 不回传 bot_open_id）。
		identity, _ := meta["bot_open_id"].(string)
		channelName, _ := meta["bot_name"].(string)
		return h.finalizeChannelBound(ctx, app, binding, payload, identity, channelName, binding.MetadataJson)
	case "fatal":
		return h.failFeishu(ctx, app, binding, payload, errMsg)
	default:
		return enqueueChannelCheck(ctx, h.store, payload, 5*time.Second)
	}
}

// takeFeishuCredentials 取出飞书凭证，按优先级两路恢复：
//  1. 扫码内存优先：经 adapter.TakeCredentials 私有交接当前进程刚回填的凭证；
//  2. DB 密文恢复：内存取不到时，只要 metadata 含 app_secret_ciphertext 即解密还原。
//
// 第 2 路覆盖扫码重试场景：扫码持久化后重试（阶段1 ① 已落库 → patch/重启失败重试时内存已空）、
// worker 重启 / 多副本（扫码内存凭证仅存于原进程，TakeCredentials 在新进程必然 false）。
func (h *ChannelCheckBindingHandler) takeFeishuCredentials(adapter channel.ChannelAdapter, payload channelLoginPayload, meta map[string]any) (channel.FeishuCredentials, bool) {
	if fa, ok := adapter.(*channel.FeishuAdapter); ok {
		if c, ok := fa.TakeCredentials(payload.AppID); ok {
			return c, true
		}
	}
	// DB 密文恢复：解密还原明文，并带出 metadata 已有的 domain / bot 信息（阶段1 ① 已写）。
	enc, _ := meta["app_secret_ciphertext"].(string)
	appID, _ := meta["app_id"].(string)
	if enc != "" && appID != "" && h.cipher != nil {
		if plain, err := h.cipher.Decrypt(enc); err == nil {
			dom, _ := meta["domain"].(string)
			botName, _ := meta["bot_name"].(string)
			botOpenID, _ := meta["bot_open_id"].(string)
			return channel.FeishuCredentials{
				AppID: appID, AppSecret: string(plain), Domain: dom,
				BotName: botName, BotOpenID: botOpenID,
			}, true
		}
	}
	return channel.FeishuCredentials{}, false
}

// failFeishu 把飞书绑定置 failed 并写审计（仿微信 failed 分支）：msg 为空时回退到通用文案。
func (h *ChannelCheckBindingHandler) failFeishu(ctx context.Context, app sqlc.App, binding sqlc.ChannelBinding, payload channelLoginPayload, msg string) error {
	safeMessage := "飞书绑定失败"
	if msg != "" {
		safeMessage = redactlog.SafeErrorMessage(errors.New(msg))
	}
	_ = h.store.SetChannelBindingStatus(ctx, sqlc.SetChannelBindingStatusParams{
		AppID:       binding.AppID,
		ChannelType: binding.ChannelType,
		Status:      domain.ChannelStatusFailed,
		LastError:   null.StringFrom(safeMessage),
	})
	return recordChannelAppAudit(ctx, h.store, app, "channel_bound", "failed", safeMessage,
		fmt.Sprintf("渠道 %s", channelLabelWorker(payload.ChannelType)),
		map[string]any{
			"channel_type": payload.ChannelType,
		})
}

// ocOpsChannelStatusClient 是飞书 health 探测所需的最小 oc-ops 能力子集
// （便于装配/单测解耦）。ChannelStatus 供阶段2 查连通态。
type ocOpsChannelStatusClient interface {
	ChannelStatus(ctx context.Context, ep ocops.Endpoint, channel string) (ocops.ChannelStatus, error)
}

// ocOpsFeishuHealthClient 经 endpoint resolver 解析 app→oc-ops 坐标，实现 FeishuHealthClient：
// FeishuStatus 调 ChannelStatus(feishu) 取连通态。health 不回传 bot_open_id，故 FeishuStatus 的
// botOpenID 恒返回空，由 worker 改用 metadata 的 bot_open_id 作 bound identity。
type ocOpsFeishuHealthClient struct {
	resolver ChannelEndpointResolver
	ops      ocOpsChannelStatusClient
}

// NewOcOpsFeishuHealthClient 构造 oc-ops 飞书 health 适配器。
func NewOcOpsFeishuHealthClient(resolver ChannelEndpointResolver, ops ocOpsChannelStatusClient) FeishuHealthClient {
	return &ocOpsFeishuHealthClient{resolver: resolver, ops: ops}
}

// FeishuStatus 解析坐标后查 oc-ops 飞书渠道健康态。
func (c *ocOpsFeishuHealthClient) FeishuStatus(ctx context.Context, appID string) (string, string, string, error) {
	ep, err := c.resolver.ResolveEndpoint(ctx, appID)
	if err != nil {
		return "", "", "", fmt.Errorf("解析 oc-ops 坐标失败: %w", err)
	}
	st, err := c.ops.ChannelStatus(ctx, ep, domain.ChannelTypeFeishu)
	if err != nil {
		return "", "", "", err
	}
	return st.PlatformState, "", st.ErrorMessage, nil
}

// finalizeChannelBound 把「渠道绑定成功」的统一收尾动作集中到一处:
//  1. MarkChannelBindingBound 写 channel_bindings.status=bound;
//  2. 触发 hermes 容器重启,让其重新读 platforms 配置加载新绑定账号
//     (微信凭证由容器内 oc-ops channel 登录端点落盘到 /opt/data/weixin/accounts/,
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
			slog.ErrorContext(ctx, "渠道绑定后重启 hermes 容器失败", "app_id", app.ID, redactlog.Err(err))
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
		slog.ErrorContext(ctx, "写渠道应用审计失败", "app_id", app.ID, slog.String(redactlog.KeyAction, action), redactlog.Err(err))
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
	// Domain 仅飞书使用：feishu | lark，决定 oc-ops 注册走哪个开放平台域。
	Domain string `json:"domain,omitempty"`
	// CheckDeadlineUnix 是连通探测的截止 Unix 秒（可选，0 = 不设上限）。
	// 钉钉等「引擎不上报 fatal、错误凭证只会一直连不上」的渠道，发起时设此 deadline，
	// 通用 check 路径在到点仍未 connected 时置 failed（超时），避免无限 pending + 无限 re-enqueue。
	// 该字段在 re-enqueue 时随 payload 透传，不随每轮轮询刷新（故不能用 binding.updated_at 替代）。
	CheckDeadlineUnix int64 `json:"check_deadline_unix,omitempty"`
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

// enqueueChannelStart 延迟重排启动登录任务，用于实例重启窗口的可恢复等待。
func enqueueChannelStart(ctx context.Context, store ChannelLoginStore, payload channelLoginPayload, delay time.Duration) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化 channel_start_login payload 失败: %w", err)
	}
	if err := store.CreateJob(ctx, sqlc.CreateJobParams{
		ID:          uuid.NewString(),
		Type:        domain.JobTypeChannelStartLogin,
		Priority:    90,
		RunAfter:    time.Now().Add(delay),
		MaxAttempts: 3,
		PayloadJson: raw,
	}); err != nil {
		return fmt.Errorf("创建 channel_start_login 任务失败: %w", err)
	}
	return nil
}

// appStatusCanRetryChannelStart 判断业务态是否仍允许等待 runtime 恢复后继续发起授权。
func appStatusCanRetryChannelStart(status string) bool {
	switch status {
	case domain.AppStatusRunning, domain.AppStatusBindingWaiting, domain.AppStatusBindingFailed:
		return true
	default:
		return false
	}
}

// channelStartErrorMessage 把渠道启动登录（BeginAuth）失败错误归约为前端可读文案。
//
// 当 oc-ops 返回 404（errors.Is(err, ocops.ErrNotFound)）时，几乎总是目标实例镜像版本
// 过旧、缺少对应渠道端点（典型：旧引擎没有飞书 /oc/channels/feishu/register 注册路由），
// 而非真正的资源不存在；此时把裸 "ocops: not found" 透传给前端会让用户误以为是系统 bug、
// 无从下手。故统一替换为「重启实例升级」的引导文案，覆盖飞书 / 微信 / 企业微信等所有渠道。
// 其余错误沿用 redactlog.SafeErrorMessage 做脱敏 + 截断。
func channelStartErrorMessage(err error) string {
	if errors.Is(err, ocops.ErrNotFound) {
		return "当前实例版本过旧，请到总览页面重启实例完成升级后重试"
	}
	return redactlog.SafeErrorMessage(err)
}

// channelLabelWorker 是 worker 包内的渠道枚举到中文映射，与 service.channelLabel 同义。
// worker 不依赖 service 包，因而在此独立维护一份；新增渠道时两份同步更新。
func channelLabelWorker(channelType string) string {
	switch channelType {
	case domain.ChannelTypeWeChat:
		return "微信"
	case domain.ChannelTypeFeishu:
		return "飞书"
	case domain.ChannelTypeWorkWeChat:
		return "企业微信"
	case domain.ChannelTypeDingTalk:
		return "钉钉"
	default:
		return channelType
	}
}

// textOrEmpty 从 null.String 取值，nil/无效时返回空串。
// channel_login handler 内的旧调用点通过此函数保持语义。
func textOrEmpty(s null.String) string {
	return s.ValueOrZero()
}
