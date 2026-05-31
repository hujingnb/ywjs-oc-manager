package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	null "github.com/guregu/null/v5"

	"oc-manager/internal/audit"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/k8sorch"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// AppInitializeStore 是 app_initialize handler 需要的最小数据访问能力。
type AppInitializeStore interface {
	GetApp(ctx context.Context, id string) (sqlc.App, error)
	GetOrganization(ctx context.Context, id string) (sqlc.Organization, error)
	GetUser(ctx context.Context, id string) (sqlc.User, error)
	SetAppNewAPIKey(ctx context.Context, arg sqlc.SetAppNewAPIKeyParams) error
	SetAppStatus(ctx context.Context, arg sqlc.SetAppStatusParams) error
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) error
	// 新增:5 阶段 handler 落进度与失败状态
	SetAppProgress(ctx context.Context, arg sqlc.SetAppProgressParams) error
	ClearAppProgress(ctx context.Context, id string) error
	MarkAppFailed(ctx context.Context, arg sqlc.MarkAppFailedParams) error
	// UpdateAppRuntimeImage 把 PullImageOnNode 返回的镜像引用和 sha256 写入 apps 表。
	UpdateAppRuntimeImage(ctx context.Context, arg sqlc.UpdateAppRuntimeImageParams) error
	// GetAssistantVersion 加载实例绑定的助手版本；初始化时必须存在否则标记失败。
	GetAssistantVersion(ctx context.Context, id string) (sqlc.AssistantVersion, error)
	// SetAppAppliedVersion 在初始化/重启成功后记录已应用的版本修订与镜像 ref，
	// 供前端 version_synced 检测使用。
	SetAppAppliedVersion(ctx context.Context, arg sqlc.SetAppAppliedVersionParams) error
	// AppHasBoundChannelBinding 用于 init 完成进入 binding_waiting 后做一次「渠道
	// 已绑定」快照判定：切换助手版本触发镜像重建时，channel_bindings 行不会被
	// 重置，凭证又落在 bind mount 持久目录，重启后 hermes 容器仍可直接加载——
	// 此场景下 app.status 应跳过 binding_waiting 直接进入 running，避免概览页
	// 长期显示「待绑定」而渠道页显示「bound」。
	AppHasBoundChannelBinding(ctx context.Context, appID string) (bool, error)
	// SetAppRuntimeToken 写入 Hermes 调 manager runtime API 的 app 级 token。
	SetAppRuntimeToken(ctx context.Context, arg sqlc.SetAppRuntimeTokenParams) error
}

// APIKeyClient 是「以业务 user 身份调 token 相关接口」的最小能力集合。
//
// 由 NewAPIClientFactory 在每次 job 处理时按 app→org 上下文构造（解密 organizations
// 的凭据密文 → 拿到 access_token + user_id）。该接口与 newapi.UserScopedClient 同形态。
type APIKeyClient interface {
	CreateAPIKey(ctx context.Context, input newapi.CreateAPIKeyInput) (newapi.APIKey, error)
	GetTokenFullKey(ctx context.Context, tokenID int64) (string, error)
	SetAPIKeyStatus(ctx context.Context, id int64, status int) error
}

// NewAPIClientFactory 在 worker 跑 job 的中间层构造 user-scoped client。
//
// 把"组织凭据 → user-scoped client"的胶水代码集中在 cmd/server 装配的 adapter 里，
// handler 只看到 APIKeyClient 接口，避免每个 handler 都重复 GetOrganization / Decrypt 的样板。
type NewAPIClientFactory interface {
	UserScopedFor(ctx context.Context, app sqlc.App) (APIKeyClient, error)
}

// AppInitializeConfig 提供 handler 运行所需的外部配置。
//
// Cipher：把 new-api 返回的完整 sk- 加密后写入 apps.newapi_key_ciphertext，
// 全程不入日志。
//
// ResolveRuntimeImage 由 cmd/server 在装配时注入，把版本 image_id 解析为
// 完整 imageRef（含 tag），是运行时镜像的唯一来源。必需依赖：未注入时
// Handle 直接 markFailed，不再有单值字段兜底。
type AppInitializeConfig struct {
	// PlatformPrompt 保留供 restart 链路（AppInputRefresher）复用，handler 本身不再使用。
	PlatformPrompt string
	// SystemPromptTemplate 保留供历史兼容，不再由 app_initialize 写入。
	SystemPromptTemplate string
	Cipher               *auth.Cipher
	// DataDir 保留供其他特定场景使用，app_initialize 不再使用。
	DataDir string
	// NewAPIBaseURL 保留供 restart 链路复用，app_initialize 不再直接使用。
	NewAPIBaseURL string
	// ContainerNetworks 保留 docker 时代字段，k8s 路径不使用。
	ContainerNetworks []string
	// AuditHelper 在 new-api 调用失败时写 audit_logs.target_type=newapi_call。
	// nil 时跳过审计，不影响主流程；生产装配应注入。
	AuditHelper *audit.NewAPIAuditHelper
	// ResolveRuntimeImage 由 cmd/server 在装配时注入，把版本 image_id 解析为
	// 完整 imageRef（含 tag），是运行时镜像的唯一来源。必需依赖：未注入时
	// Handle 直接 markFailed，不再有单值字段兜底。
	ResolveRuntimeImage func(imageID string) (ref string, ok bool)
	// ManagerRuntimeBaseURL 保留供 restart 链路复用，app_initialize 不再直接使用。
	ManagerRuntimeBaseURL string
}

// AppInitializeK8sConfig 是 k8s 路径需要的最小配置子集。
// 从 internal/config.KubernetesConfig 对应字段提取，handler 包独立定义
// 避免反向依赖 internal/config 包。
type AppInitializeK8sConfig struct {
	// OpsImage 是 spec-A1 ops 镜像 ref（initContainer/sidecar）。
	OpsImage string
	// BootstrapBaseURL 是 pod 调 bootstrap 的基址（拼 /internal/apps/<id>/bootstrap）。
	BootstrapBaseURL string
	// ImagePullSecret 是拉取私有镜像的 Secret 名（如 acr-pull）。
	ImagePullSecret string
	// Resources 是 app pod 的资源 requests/limits。
	Resources AppInitializeK8sResources
	// Proxy 为需直连外网的 hermes/oc-ops 容器注入代理 env（本地 k3d 无外网出口时用；
	// 生产留空不注入）。
	Proxy AppInitializeK8sProxy
}

// AppInitializeK8sProxy 是注入 app pod 容器的代理环境变量（留空不注入对应项）。
type AppInitializeK8sProxy struct {
	HTTPProxy  string
	HTTPSProxy string
	NoProxy    string
}

// AppInitializeK8sResources 是 pod 资源 requests/limits 配置。
type AppInitializeK8sResources struct {
	Requests AppInitializeK8sResourceSpec
	Limits   AppInitializeK8sResourceSpec
}

// AppInitializeK8sResourceSpec 是单组 CPU/内存配额（k8s quantity 字符串）。
type AppInitializeK8sResourceSpec struct {
	CPU    string
	Memory string
}

// AppInitializeHandler 编排应用初始化全流程（k8s 路径）。
//
// 顺序：
//  1. 加载 app 上下文，幂等检查；
//  2. 校验实例绑定版本，解析 hermes 镜像 ref（phasePullRuntimeImage 状态）；
//  3. ensureAPIKey（phasePrepare 状态）；
//  4. EnsureAppRuntimeToken 拿 control token 明文（phasePrepare 状态）；
//  5. EnsureApp：渲染 AppSpec → k8s Deployment + Service + Secret（phaseCreate 状态）；
//  6. WaitReady：等 pod Ready（phaseStart 状态）；
//  7. → binding_waiting → promoteIfChannelBound。
//
// 任意一步失败立即冒泡，由 worker 重试或入 failed；状态机字段只在显式步骤里单独写。
type AppInitializeHandler struct {
	store   AppInitializeStore
	factory NewAPIClientFactory
	cfg     AppInitializeConfig
	// orch 是 k8s app 编排接口；nil 时 phaseCreate/phaseStart 直接跳过（测试装配 / Task14 前）。
	orch k8sorch.Orchestrator
	// k8sCfg 是渲染 AppSpec 所需的 k8s 配置（从 config.KubernetesConfig 提取）。
	k8sCfg AppInitializeK8sConfig
}

// NewAppInitializeHandler 创建 handler。
// k8s 编排器经 SetOrchestrator 单独注入（orch + 渲染 AppSpec 所需的 k8sCfg），
// 不在构造期传入：未注入时 phaseCreate/phaseStart 直接跳过，便于单测装配最小 handler。
func NewAppInitializeHandler(
	store AppInitializeStore,
	factory NewAPIClientFactory,
	cfg AppInitializeConfig,
) *AppInitializeHandler {
	return &AppInitializeHandler{
		store:   store,
		factory: factory,
		cfg:     cfg,
	}
}

// SetOrchestrator 注入 k8s 编排器与配置。
// 生产环境由 cmd/server 装配时注入真实 KubernetesAdapter + k8sCfg；
// nil 时 phaseCreate/phaseStart 直接跳过（测试/早期装配兼容）。
func (h *AppInitializeHandler) SetOrchestrator(orch k8sorch.Orchestrator, k8sCfg AppInitializeK8sConfig) {
	h.orch = orch
	h.k8sCfg = k8sCfg
}

// readyTimeout 是 WaitReady 等待 pod Ready 的总时长上限。
// k8s 镜像拉取 + 容器启动通常在分钟级；5 分钟足以覆盖慢网络拉取场景，
// 同时能在 pod 卡死时将实例收敛到 error 等待重试。
const readyTimeout = 5 * time.Minute

// Handle 是 worker 调用入口。
// 4 阶段串行推进（k8s 路径）：版本校验 + ensureAPIKey/token → EnsureApp → WaitReady → binding_waiting。
// 任何失败收敛到 status=error 并写入 last_error_status 记录来源阶段。
func (h *AppInitializeHandler) Handle(ctx context.Context, job sqlc.Job) error {
	if job.Type != domain.JobTypeAppInitialize {
		return fmt.Errorf("非 app_initialize 任务: %s", job.Type)
	}
	payload, err := decodePayload(job.PayloadJson)
	if err != nil {
		return err
	}
	app, err := h.store.GetApp(ctx, payload.AppID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("应用 %s 不存在", payload.AppID)
		}
		return fmt.Errorf("查询应用失败: %w", err)
	}
	// 已离开初始化阶段直接成功（幂等）。
	// binding_waiting 分支再做一次「渠道已绑定」自愈：上一次切换助手版本+重启
	// 触发的镜像重建在 transitionTo 阶段已经把行推到 binding_waiting，但若此时
	// channel_bindings 已是 bound（凭证保留在 k8s Secret，hermes 容器重启后无需
	// 重新扫码），就直接续推到 running，让概览页与渠道页状态收敛。
	if app.Status == domain.AppStatusBindingWaiting {
		if err := h.promoteIfChannelBound(ctx, &app); err != nil {
			return err
		}
		return nil
	}
	if app.Status == domain.AppStatusRunning {
		return nil
	}

	// 加载实例绑定的助手版本：未绑定版本的实例无法初始化，直接标记失败。
	if !app.VersionID.Valid {
		return h.markFailed(ctx, &app, domain.AppStatusPullingRuntimeImage, errors.New("实例未绑定助手版本，无法初始化"))
	}
	version, err := h.store.GetAssistantVersion(ctx, app.VersionID.String)
	if err != nil {
		return h.markFailed(ctx, &app, domain.AppStatusPullingRuntimeImage, fmt.Errorf("加载助手版本失败: %w", err))
	}

	// 解析版本镜像 ref：运行时镜像严格由绑定版本经 ResolveRuntimeImage 从
	// runtime_images 列表解析。ResolveRuntimeImage 是必需依赖，未注入属于装配
	// 错误，直接标记失败而非静默兜底。
	if h.cfg.ResolveRuntimeImage == nil {
		return h.markFailed(ctx, &app, domain.AppStatusPullingRuntimeImage,
			errors.New("ResolveRuntimeImage 未注入，无法解析运行时镜像"))
	}
	imageRef, ok := h.cfg.ResolveRuntimeImage(version.ImageID)
	if !ok {
		return h.markFailed(ctx, &app, domain.AppStatusPullingRuntimeImage,
			fmt.Errorf("版本镜像 %s 未在配置中", version.ImageID))
	}

	reporter := newProgressReporter(app.ID, h.store)

	// k8s 4 阶段定义：每阶段先 transitionTo 推 status，再 run 跑实际工作。
	// version 与 imageRef 通过闭包注入各阶段，避免在 handler 结构体上存储
	// 每次 Handle 调用的私有状态（防止并发安全问题）。
	steps := []struct {
		phase string
		run   func(context.Context, *sqlc.App, appInitializePayload, *progressReporter) error
	}{
		// 阶段1（pulling_runtime_image）：版本与镜像 ref 校验已在前置步骤完成，此处为空占位，
		// 保留状态机阶段标记以便 markFailed 在版本加载失败时记录正确的 last_error_status。
		// k8s 镜像拉取由 imagePullPolicy 接管，不需要 manager 主动 pull。
		{domain.AppStatusPullingRuntimeImage, func(ctx context.Context, app *sqlc.App, p appInitializePayload, r *progressReporter) error {
			// 版本校验与镜像 ref 解析已在进入 steps 循环前完成，此阶段仅推状态。
			return nil
		}},
		// 阶段2（preparing_runtime）：ensureAPIKey + EnsureAppRuntimeToken 拿 control token 明文。
		{domain.AppStatusPreparingRuntime, func(ctx context.Context, app *sqlc.App, p appInitializePayload, r *progressReporter) error {
			return h.phasePrepare(ctx, app)
		}},
		// 阶段3（creating_container）：EnsureApp 渲染并 apply k8s Deployment + Service + Secret。
		{domain.AppStatusCreatingContainer, func(ctx context.Context, app *sqlc.App, p appInitializePayload, r *progressReporter) error {
			return h.phaseCreate(ctx, app, imageRef)
		}},
		// 阶段4（starting）：WaitReady 等待 pod Ready。
		{domain.AppStatusStarting, func(ctx context.Context, app *sqlc.App, p appInitializePayload, r *progressReporter) error {
			return h.phaseStart(ctx, app)
		}},
	}

	for _, step := range steps {
		if err := h.transitionTo(ctx, &app, step.phase, reporter); err != nil {
			return h.markFailed(ctx, &app, step.phase, err)
		}
		if err := step.run(ctx, &app, payload, reporter); err != nil {
			return h.markFailed(ctx, &app, step.phase, err)
		}
	}

	if err := h.transitionTo(ctx, &app, domain.AppStatusBindingWaiting, reporter); err != nil {
		return h.markFailed(ctx, &app, domain.AppStatusStarting, err)
	}

	// 初始化成功后记录已应用的版本修订与镜像 ref，供前端 version_synced 检测使用。
	// 写库失败时走 markFailed：把 app 收敛到 status=error 并记录 last_error_status，
	// 避免行卡在 binding_waiting 而 worker 又把 job 当失败处理。
	if err := h.store.SetAppAppliedVersion(ctx, sqlc.SetAppAppliedVersionParams{
		ID:                     app.ID,
		AppliedVersionRevision: version.Revision,
		AppliedImageRef:        imageRef,
	}); err != nil {
		return h.markFailed(ctx, &app, domain.AppStatusBindingWaiting, fmt.Errorf("记录已应用版本信息失败: %w", err))
	}

	// 镜像重建场景下，channel_bindings 上一次的 bound 状态不会被清空，凭证又落在
	// k8s Secret 被新 pod 复用，无需用户重新扫码。若发现已 bound 就直接续推
	// 到 running，让概览页与渠道页状态一致——否则会出现「渠道页 bound、概览页
	// 待绑定」的卡死视图。
	if err := h.promoteIfChannelBound(ctx, &app); err != nil {
		return h.markFailed(ctx, &app, domain.AppStatusBindingWaiting, err)
	}

	return h.writeInitAuditLog(ctx, app, job, payload)
}

// phasePrepare：ensureAPIKey + EnsureAppRuntimeToken。
// 两步都已有幂等（已 active 跳过 ensureAPIKey，已有密文跳过 token 生成），
// 重启重入直接跑安全。
func (h *AppInitializeHandler) phasePrepare(ctx context.Context, app *sqlc.App) error {
	// 确保 new-api api_key 存在并写入加密密文。
	if _, err := h.ensureAPIKey(ctx, app); err != nil {
		return err
	}
	// 确保 per-app control token 存在（三用：bootstrap + oc-kb + oc-ops）。
	// 返回的 app 包含最新 runtime_token_* 字段；token 明文在 phaseCreate buildAppSpec 时使用。
	updated, _, err := service.EnsureAppRuntimeToken(ctx, h.store, h.cfg.Cipher, *app)
	if err != nil {
		return fmt.Errorf("确保 runtime token 失败: %w", err)
	}
	*app = updated
	return nil
}

// phaseCreate：buildAppSpec → EnsureApp（渲染并 apply k8s Deployment + Service + Secret）。
// orch 未注入时直接跳过（测试装配 / Task14 前）。
func (h *AppInitializeHandler) phaseCreate(ctx context.Context, app *sqlc.App, imageRef string) error {
	if h.orch == nil {
		return nil
	}
	// 从 app 的 runtime_token_ciphertext 解密取明文 control token，
	// 用于写入 k8s Secret，供 pod 启动时鉴权 bootstrap API 和 oc-ops 调用。
	controlToken, err := h.decryptRuntimeToken(app)
	if err != nil {
		return fmt.Errorf("解密 runtime token 失败（phaseCreate）: %w", err)
	}
	spec := h.buildAppSpec(*app, imageRef, controlToken)
	if err := h.orch.EnsureApp(ctx, spec); err != nil {
		return fmt.Errorf("k8s EnsureApp 失败: %w", err)
	}
	return nil
}

// phaseStart：WaitReady 等待 pod Ready（带 readyTimeout）。
// orch 未注入时直接跳过（测试装配 / Task14 前）。
func (h *AppInitializeHandler) phaseStart(ctx context.Context, app *sqlc.App) error {
	if h.orch == nil {
		return nil
	}
	if err := h.orch.WaitReady(ctx, app.ID, readyTimeout); err != nil {
		return fmt.Errorf("等待 k8s pod Ready 失败: %w", err)
	}
	return nil
}

// buildAppSpec 把 app + 解析出的 hermes 镜像 + control token 渲染为 k8sorch.AppSpec。
func (h *AppInitializeHandler) buildAppSpec(app sqlc.App, hermesImage, controlToken string) k8sorch.AppSpec {
	// bootstrapURL 由 BootstrapBaseURL 拼 appID 构成，pod 调此地址拉初始化配置。
	bootstrapURL := strings.TrimRight(h.k8sCfg.BootstrapBaseURL, "/") + "/internal/apps/" + app.ID + "/bootstrap"
	return k8sorch.AppSpec{
		AppID:           app.ID,
		HermesImage:     hermesImage,
		OpsImage:        h.k8sCfg.OpsImage,
		ControlToken:    controlToken,
		BootstrapURL:    bootstrapURL,
		ImagePullSecret: h.k8sCfg.ImagePullSecret,
		Resources: k8sorch.ResourceLimits{
			RequestsCPU:    h.k8sCfg.Resources.Requests.CPU,
			RequestsMemory: h.k8sCfg.Resources.Requests.Memory,
			LimitsCPU:      h.k8sCfg.Resources.Limits.CPU,
			LimitsMemory:   h.k8sCfg.Resources.Limits.Memory,
		},
		Proxy: k8sorch.ProxyEnv{
			HTTPProxy:  h.k8sCfg.Proxy.HTTPProxy,
			HTTPSProxy: h.k8sCfg.Proxy.HTTPSProxy,
			NoProxy:    h.k8sCfg.Proxy.NoProxy,
		},
	}
}

// decryptRuntimeToken 从 app 的 RuntimeTokenCiphertext 解密取明文 control token。
// 调用方保证在 EnsureAppRuntimeToken 之后调用，密文必须存在。
// 使用 handler 持有的 cfg.Cipher，与 EnsureAppRuntimeToken 加密时用的是同一把 cipher。
func (h *AppInitializeHandler) decryptRuntimeToken(app *sqlc.App) (string, error) {
	if !app.RuntimeTokenCiphertext.Valid || app.RuntimeTokenCiphertext.String == "" {
		return "", fmt.Errorf("app %s runtime_token_ciphertext 为空，无法解密", app.ID)
	}
	if h.cfg.Cipher == nil {
		return "", fmt.Errorf("cipher 未配置，无法解密 runtime token")
	}
	plain, err := h.cfg.Cipher.Decrypt(app.RuntimeTokenCiphertext.String)
	if err != nil {
		return "", fmt.Errorf("解密 runtime token 失败: %w", err)
	}
	return string(plain), nil
}

// promoteIfChannelBound 在 status=binding_waiting 时探测该 app 是否已有 bound 渠道，
// 若有则把 status 推到 running。
//
// 触发场景：切换助手版本 + 重启 → 镜像变更走 app_runtime_ops 重建分支 → 入队新的
// app_initialize → 重建 pod 后走到 binding_waiting。整个流程不会重置渠道行，凭证又
// 在 k8s Secret 里持续可用，所以最终态应当是 running 而不是 binding_waiting。
//
// 仅在 status=binding_waiting 时调用，调用方负责前置判断；状态机转移不合法时
// 冒泡 error，由调用方决定 markFailed 还是直接返回。
func (h *AppInitializeHandler) promoteIfChannelBound(ctx context.Context, app *sqlc.App) error {
	if app.Status != domain.AppStatusBindingWaiting {
		return nil
	}
	hasBound, err := h.store.AppHasBoundChannelBinding(ctx, app.ID)
	if err != nil {
		return fmt.Errorf("查询渠道绑定状态失败: %w", err)
	}
	if !hasBound {
		return nil
	}
	if err := domain.EnsureAppTransition(app.Status, domain.AppStatusRunning); err != nil {
		return fmt.Errorf("校验状态转移失败: %w", err)
	}
	if err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusRunning}); err != nil {
		return fmt.Errorf("推进应用状态到 running 失败: %w", err)
	}
	// SetAppStatus 是 :exec，不返回行；从 DB 重新读取最新状态。
	updated, err := h.store.GetApp(ctx, app.ID)
	if err != nil {
		return fmt.Errorf("读取更新后的 app 失败: %w", err)
	}
	*app = updated
	return nil
}

// transitionTo 推 status 并清空 progress_*；违反状态机直接返回 error，
// 由调用方决定是否 markFailed。
func (h *AppInitializeHandler) transitionTo(ctx context.Context, app *sqlc.App, to string, reporter *progressReporter) error {
	if app.Status == to {
		// 重启重入时已经处于目标阶段，跳过一次写库
		return nil
	}
	if err := domain.EnsureAppTransition(app.Status, to); err != nil {
		return err
	}
	if err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: to}); err != nil {
		return fmt.Errorf("写入应用状态失败: %w", err)
	}
	// SetAppStatus 是 :exec；读回最新状态。
	updated, err := h.store.GetApp(ctx, app.ID)
	if err != nil {
		return fmt.Errorf("读取更新后的 app 失败: %w", err)
	}
	*app = updated
	reporter.FlushReset(ctx)
	return nil
}

// markFailed 把 status 推到 error，同时写入来源 phase 与错误文本，
// 让前端能展示"在哪一步失败"和"为什么失败"两层信息。
// 即便写库失败也返回原 cause，避免吞掉真实错误。
func (h *AppInitializeHandler) markFailed(ctx context.Context, app *sqlc.App, phase string, cause error) error {
	if err := h.store.MarkAppFailed(ctx, sqlc.MarkAppFailedParams{
		ID:               app.ID,
		LastErrorStatus:  null.StringFrom(phase),
		LastErrorMessage: null.StringFrom(cause.Error()),
	}); err != nil {
		return fmt.Errorf("%w (写入失败状态也失败: %v)", cause, err)
	}
	return cause
}

// writeInitAuditLog 把 Handle 末尾的审计日志逻辑独立出来，Handle 完成 binding_waiting
// 转移后调用一次。
func (h *AppInitializeHandler) writeInitAuditLog(ctx context.Context, app sqlc.App, job sqlc.Job, payload appInitializePayload) error {
	// k8s 路径无节点概念，audit metadata 只保留 job_id。
	auditMetadata, err := json.Marshal(map[string]any{
		"job_id": job.ID,
	})
	if err != nil {
		return fmt.Errorf("序列化应用初始化审计元数据失败: %w", err)
	}
	if err := h.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
		ID:           uuid.NewString(),
		ActorRole:    "system",
		OrgID:        null.StringFrom(app.OrgID),
		TargetType:   "app",
		TargetID:     app.ID,
		Action:       "initialize",
		Result:       "succeeded",
		MetadataJson: auditMetadata,
		// 不填 DetailMessage：initialize 的资源列已展示 app 名，详情列冗余。
	}); err != nil {
		return fmt.Errorf("写入应用初始化审计日志失败: %w", err)
	}
	return nil
}

// ensureAPIKey 走「以组织业务 user 身份创 token + 拉完整 sk-」流程，加密落库后返回明文 sk-。
//
// 已经 active 的应用直接读 ciphertext 解密返回，避免重复创建。
// 解密失败 / 拉 key 失败都直接报错；不再有"全局 fallback sk-"的兜底路径。
func (h *AppInitializeHandler) ensureAPIKey(ctx context.Context, app *sqlc.App) (string, error) {
	if app.ApiKeyStatus == domain.APIKeyStatusActive {
		if !app.NewapiKeyCiphertext.Valid || app.NewapiKeyCiphertext.String == "" {
			return "", fmt.Errorf("应用 %s 已 active 但 newapi_key_ciphertext 为空", app.ID)
		}
		return decryptCiphertext(app.NewapiKeyCiphertext.String, h.cfg.Cipher)
	}
	if h.factory == nil {
		return "", fmt.Errorf("new-api client factory 未配置，无法创建 api_key")
	}
	if h.cfg.Cipher == nil {
		return "", fmt.Errorf("cipher 未配置，无法加密 api_key")
	}
	client, err := h.factory.UserScopedFor(ctx, *app)
	if err != nil {
		return "", fmt.Errorf("构造 user-scoped client 失败: %w", err)
	}

	// 应用级 token 默认 unlimited_quota=true：manager 不在 token 层面做限额（spec §10），
	// 计费与额度归 new-api 的 user 级管理。
	// keyName 是 manager 与 new-api 之间反查 token 的唯一约定（"app-" + uuid），
	// 抽局部变量后既作为 CreateAPIKey 入参，也写入 apps.newapi_key_name 落库，
	// 让 usage 查询直接读字段而不再依赖 "token name 与 app.ID 同值" 的隐式假设。
	keyName := fmt.Sprintf("app-%s", app.ID)
	key, err := client.CreateAPIKey(ctx, newapi.CreateAPIKeyInput{
		Name:       keyName,
		Models:     []string{},
		UnlimitedQ: true,
	})
	if err != nil {
		if h.cfg.AuditHelper != nil {
			h.cfg.AuditHelper.RecordFailure(ctx, audit.NewAPIFailureContext{
				OrgID:    app.OrgID,
				Endpoint: "POST /api/token/",
				Err:      err,
			})
		}
		return "", fmt.Errorf("调用 new-api 创建 api_key 失败: %w", err)
	}
	if key.ID == 0 {
		return "", fmt.Errorf("new-api CreateAPIKey 返回 token id=0")
	}

	fullKey, err := client.GetTokenFullKey(ctx, key.ID)
	if err != nil {
		if h.cfg.AuditHelper != nil {
			h.cfg.AuditHelper.RecordFailure(ctx, audit.NewAPIFailureContext{
				OrgID:    app.OrgID,
				Endpoint: fmt.Sprintf("POST /api/token/%d/key", key.ID),
				Err:      err,
			})
		}
		return "", fmt.Errorf("调用 new-api 取完整 sk- 失败: %w", err)
	}

	ciphertext, err := h.cfg.Cipher.Encrypt([]byte(fullKey))
	if err != nil {
		return "", fmt.Errorf("加密 api_key 失败: %w", err)
	}
	if err := h.store.SetAppNewAPIKey(ctx, sqlc.SetAppNewAPIKeyParams{
		ID:                  app.ID,
		NewapiKeyID:         null.StringFrom(fmt.Sprintf("%d", key.ID)),
		NewapiKeyCiphertext: null.StringFrom(ciphertext),
		ApiKeyStatus:        domain.APIKeyStatusActive,
		// 显式落 newapi_key_name：与上面 CreateAPIKey 用的 keyName 保持一致，
		// 让后续 usage 查询不必再次拼 "app-<uuid>"，直接从 apps 表读字段即可。
		NewapiKeyName: null.StringFrom(keyName),
	}); err != nil {
		return "", fmt.Errorf("写入 api_key 失败: %w", err)
	}
	// SetAppNewAPIKey 是 :exec；读回最新状态。
	updated, err := h.store.GetApp(ctx, app.ID)
	if err != nil {
		return "", fmt.Errorf("读取更新后的 app 失败: %w", err)
	}
	*app = updated
	return fullKey, nil
}

// decryptCiphertext 把 newapi_key_ciphertext 解为明文 sk-；cipher 必须非 nil（生产强制装配）。
func decryptCiphertext(ciphertext string, cipher *auth.Cipher) (string, error) {
	if cipher == nil {
		return "", fmt.Errorf("cipher 未配置，无法解密 api_key")
	}
	if ciphertext == "" {
		return "", fmt.Errorf("api_key 密文为空")
	}
	plain, err := cipher.Decrypt(ciphertext)
	if err != nil {
		return "", fmt.Errorf("解密 api_key 失败: %w", err)
	}
	return string(plain), nil
}

// appInitializePayload 是 app_initialize job 的 JSON 载荷。
// k8s 路径已无节点概念，不再需要 runtime_node 字段；
// payload 只传 app_id，handler 通过 GetApp 拿到完整 app 行。
type appInitializePayload struct {
	AppID string `json:"app_id"`
}

func decodePayload(raw []byte) (appInitializePayload, error) {
	var payload appInitializePayload
	if len(raw) == 0 {
		return payload, fmt.Errorf("app_initialize payload 为空")
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return payload, fmt.Errorf("解析 payload 失败: %w", err)
	}
	if payload.AppID == "" {
		return payload, fmt.Errorf("payload 缺少 app_id")
	}
	return payload, nil
}

// newUUID 生成新的 UUID 字符串，供需要新 ID 的场景使用。
func newUUID() string {
	return uuid.NewString()
}
