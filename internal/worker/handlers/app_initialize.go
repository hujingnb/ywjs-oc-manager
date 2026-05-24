package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dockerclient "github.com/docker/docker/client"
	"oc-manager/internal/audit"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/hermes"
	"oc-manager/internal/integrations/newapi"

	runtimepkg "oc-manager/internal/integrations/runtime"
	"oc-manager/internal/runtime/imagecoord"
	"oc-manager/internal/store/sqlc"
)

// AppInitializeStore 是 app_initialize handler 需要的最小数据访问能力。
type AppInitializeStore interface {
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	GetOrganization(ctx context.Context, id pgtype.UUID) (sqlc.Organization, error)
	GetUser(ctx context.Context, id pgtype.UUID) (sqlc.User, error)
	GetRuntimeNode(ctx context.Context, id pgtype.UUID) (sqlc.RuntimeNode, error)
	SetAppNewAPIKey(ctx context.Context, arg sqlc.SetAppNewAPIKeyParams) (sqlc.App, error)
	SetAppContainer(ctx context.Context, arg sqlc.SetAppContainerParams) (sqlc.App, error)
	SetAppStatus(ctx context.Context, arg sqlc.SetAppStatusParams) (sqlc.App, error)
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error)
	// 新增:5 阶段 handler 落进度与失败状态
	SetAppProgress(ctx context.Context, arg sqlc.SetAppProgressParams) (sqlc.App, error)
	ClearAppProgress(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	MarkAppFailed(ctx context.Context, arg sqlc.MarkAppFailedParams) (sqlc.App, error)
	// UpdateAppRuntimeImage 把 PullImageOnNode 返回的镜像引用和 sha256 写入 apps 表。
	UpdateAppRuntimeImage(ctx context.Context, arg sqlc.UpdateAppRuntimeImageParams) (sqlc.App, error)
	// GetAssistantVersion 加载实例绑定的助手版本；初始化时必须存在否则标记失败。
	GetAssistantVersion(ctx context.Context, id pgtype.UUID) (sqlc.AssistantVersion, error)
	// SetAppAppliedVersion 在初始化/重启成功后记录已应用的版本修订与镜像 ref，
	// 供前端 version_synced 检测使用。
	SetAppAppliedVersion(ctx context.Context, arg sqlc.SetAppAppliedVersionParams) (sqlc.App, error)
	// AppHasBoundChannelBinding 用于 init 完成进入 binding_waiting 后做一次「渠道
	// 已绑定」快照判定：切换助手版本触发镜像重建时，channel_bindings 行不会被
	// 重置，凭证又落在 bind mount 持久目录，重启后 hermes 容器仍可直接加载——
	// 此场景下 app.status 应跳过 binding_waiting 直接进入 running，避免概览页
	// 长期显示「待绑定」而渠道页显示「bound」。
	AppHasBoundChannelBinding(ctx context.Context, appID pgtype.UUID) (bool, error)
}

// ContainerCreator 抽象通过 agent docker 代理创建容器的能力。
// 与 runtime.AgentBackedAdapter 的 CreateContainer 签名兼容；测试中可替换为内存桩。
type ContainerCreator interface {
	CreateContainer(ctx context.Context, nodeID string, spec runtimepkg.ContainerSpec) (runtimepkg.ContainerInfo, error)
}

// AgentDirInitializer 抽象在节点 agent 上创建应用目录的能力。
// Hermes 时代 apps/<id>/.hermes 目录由 manager 本地写入后再 bind mount；
// 节点 agent 侧无需预建子目录，仅在需要时调 InitAppDirs 兼容旧路径。
type AgentDirInitializer interface {
	InitAppDirs(ctx context.Context, nodeID, appID string) error
}

// AppInputUploader 抽象在节点 agent 上写应用输入文件 (apps/<id>/input/) 的能力。
// 由 internal/integrations/runtime.AgentBackedAdapter 实现 (内部转发到
// agent.AgentFileClient.UploadAppInputFile, 命中 agent input/file 路由)。
//
// 写入对象是 manifest.yaml + resources/*.md 这一层「容器外可读的输入资源」;
// 镜像 oc-entrypoint 在容器启动时再把它们翻译成 hermes 自有 schema (SOUL.md /
// config.yaml / .env / skills/<name>/SKILL.md)。manager 端不再直接生成 hermes
// 内部文件。
//
// nil 装配时 phasePrepare 内 WriteAppInput 调用会直接 panic, 因此生产装配必须注入。
type AppInputUploader interface {
	UploadAppInputFile(ctx context.Context, nodeID, appID, relPath string, content io.Reader) error
}

// KnowledgeReader 抽象 manager 主副本的读能力,供 writeKnowledgeIntoInput 在容器启动前
// 把组织/应用知识库主副本原样递归上传到 apps/<id>/input/resources/knowledge/{org,app}/。
//
// WalkFiles 递归遍历 prefix(如 "org/<id>/knowledge")下所有普通文件,每个文件
// 回调一次,relPath 相对 prefix、统一 '/' 分隔。
// Open 打开主副本中的指定文件;调用方负责关闭。
//
// nil 装配时 writeKnowledgeIntoInput 直接跳过, 使旧装配 / 测试装配仍可工作。
type KnowledgeReader interface {
	WalkFiles(prefix string, fn func(relPath string, size int64) error) error
	Open(masterPath string) (io.ReadCloser, int64, error)
}

// SkillBlobReader 抽象「读取 manager 文件系统上版本 skill tar 主副本」的能力。
// relPath 是版本 skills_json 中存储的 file_path（相对 manager 数据根目录）。
// nil 装配时 writeSkillsIntoInput 跳过推送，返回空路径列表，保持旧装配/测试装配兼容。
type SkillBlobReader interface {
	OpenSkill(relPath string) (io.ReadCloser, error)
}

// ContainerStarter 抽象创建后启动容器的能力。
// 5 阶段 handler 在 phaseStart 内会先 InspectContainer 看 State,
// 已 running 跳过 start 直接进健康检查;exited / created 才 Start。
// 与 app_runtime_ops.go 的 ContainerLifecycle 不重叠:那个接口要求 Start/Stop/Restart/Remove
// 四个方法,初始化阶段只需要 Start,因此独立小接口便于测试 mock。
type ContainerStarter interface {
	StartContainer(ctx context.Context, nodeID, containerID string) error
}

// ContainerState 是 phaseStart 用的最小容器状态视图。
// 与 runtime.AgentBackedAdapter 的 ContainerInspect 返回结构对齐时,
// 在 wiring 处用类型断言适配;adapter 未实现 InspectContainer 时,
// phaseStart 退回直接 Start(原行为)。
type ContainerState struct {
	Running  bool
	HealthOK bool
}

// HermesHealthChecker 是 ContainerStarter 的扩展：实现该接口的 starter 在容器启动后
// 等 docker HEALTHCHECK 报 healthy 才返回。AgentBackedAdapter 实现此接口（WaitContainerHealthy）。
// handler 通过类型断言探测，未实现的旧实现仍能跑通——
// 只是状态机会立即推到 binding_waiting，等后续健康检查任务再确认状态。
type HermesHealthChecker interface {
	WaitContainerHealthy(ctx context.Context, nodeID, containerID string, timeout time.Duration) error
}

// NodeDockerProvider 抽象按 nodeID 获取 Docker SDK 客户端（指向该节点 agent docker proxy）的能力。
// 生产装配由 AgentBackedAdapter.DockerClientForNode 实现；测试可注入内存桩。
type NodeDockerProvider interface {
	DockerClientForNode(ctx context.Context, nodeID string) (*dockerclient.Client, error)
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
// PlatformPrompt: 平台层默认 prompt 内容,作为 resources/platform-rules.md 写入
// apps/<id>/input/; 由 oc-entrypoint 在容器启动时与 organization / application
// rules 合并生效。
//
// SystemPromptTemplate: persona / SOUL 模板字符串,保留供 oc-entrypoint 历史
// 行为兼容; hermes-agent-pull 切换后实际写入 resources/persona.md 的内容由
// app.persona / org.persona 决定,此字段仅在装配链路上保留入口。
//
// Cipher：把 new-api 返回的完整 sk- 加密后写入 apps.newapi_key_ciphertext，
// 全程不入日志。
//
// DataDir 字段已从 Hermes 文件分发路径移除: 应用输入文件现在通过
// AppInputUploader.UploadAppInputFile 上传到目标节点 agent,
// 不再写入 manager 本机目录。DataDir 保留供其他特定场景（如 workspaceService）使用。
type AppInitializeConfig struct {
	PlatformPrompt       string
	SystemPromptTemplate string
	Cipher               *auth.Cipher
	// DataDir 是 manager 宿主机上的数据根目录，仅供其他特定场景使用。
	// 应用输入文件分发已走 UploadAppInputFile，不再使用此字段。
	DataDir string
	// NewAPIBaseURL 是 new-api 内网访问 URL(不含 /v1), 用于写入 manifest.yaml 的
	// credentials.openai.base_url; 容器内 hermes 走 +"/v1" 后端访问 new-api。
	NewAPIBaseURL string
	// ContainerNetworks 决定 manager 创建容器时连接哪些 docker network；
	// 必须包含 new-api 所在的 network，否则 Hermes 容器无法路由到 new-api。
	ContainerNetworks []string
	// LLM 是 Hermes 容器内 agent 的模型配置；
	// BaseURL 写入 config.yaml base_url；DefaultProvider/DefaultModel 用于 config.yaml model.default。
	// 任一字段为空时跳过对应注入，便于旧测试装配。
	LLM AppInitializeLLMConfig
	// AuditHelper 在 new-api 调用失败时写 audit_logs.target_type=newapi_call。
	// nil 时跳过审计，不影响主流程；生产装配应注入。
	AuditHelper *audit.NewAPIAuditHelper
	// ResolveRuntimeImage 由 cmd/server 在装配时注入，把版本 image_id 解析为
	// 完整 imageRef（含 tag），是运行时镜像的唯一来源。必需依赖：未注入时
	// Handle 直接 markFailed，不再有单值字段兜底。
	ResolveRuntimeImage func(imageID string) (ref string, ok bool)
	// SkillBlobs 提供版本 skill tar 主副本的读能力，用于 writeSkillsIntoInput 把
	// skill 推送到节点 input/resources/skills/。nil 时跳过 skill 推送（测试/旧装配兼容）。
	SkillBlobs SkillBlobReader
}

// AppInitializeLLMConfig 是 AppInitializeConfig.LLM 的类型，与 internal/config 的
// HermesLLMConfig 同语义；handler 包独立定义避免反向依赖 internal/config 包。
type AppInitializeLLMConfig struct {
	BaseURL         string
	DefaultProvider string
	DefaultModel    string
}

// AppInitializeHandler 编排应用初始化全流程。
//
// 顺序：
//  1. 加载 app/org/owner/runtime_node 上下文；
//  2. 幂等：状态 ∈ {running, binding_waiting} 直接返回成功；
//  3. 调 imagePullCoord 通过 agent docker proxy 在目标节点直接 pull hermes runtime 镜像；
//  4. 调 AgentDirInitializer 在节点上准备 apps/<id>/ 目录；
//  5. api_key 不 active 时调 new-api 创建并 cipher.Encrypt 写库（ensureAPIKey）；
//  6. 通过 AppInputUploader 写 apps/<id>/input/ 的 manifest.yaml + resources/*.md
//     + 知识库主副本(使用步骤 5 的真实 token,避免 HTTP 401);容器内 oc-entrypoint
//     在启动时把这些输入文件翻译成 hermes 自有 schema;
//  7. container_id 为空时调 ContainerCreator.CreateContainer，把 ID/Name 写库；
//  8. 调 ContainerStarter.StartContainer 启动容器；
//  9. starter 实现 HermesHealthChecker 时等 docker HEALTHCHECK 报 healthy；
//
// 10. 推 status=binding_waiting，由 channel 流程接管后续。
//
// 任意一步失败立即冒泡，由 worker 重试或入 failed；状态机字段只在显式步骤里单独写。
type AppInitializeHandler struct {
	store AppInitializeStore
	dirs  AgentDirInitializer
	// inputFiles 是 hermes-agent-pull 切换后的主入口: 通过 agent input/file 路由
	// 把 manifest.yaml + resources/*.md + 知识库主副本写到 apps/<id>/input/。
	// 生产装配必须注入; nil 时 phasePrepare 写文件阶段直接报错。
	inputFiles AppInputUploader
	knowledge  KnowledgeReader
	containers ContainerCreator
	starter    ContainerStarter
	factory    NewAPIClientFactory
	cfg        AppInitializeConfig
	// imagePullCoord 负责跨 manager 实例对同一 (nodeID, imageRef) 做 single-flight pull。
	// nil 时 phasePullRuntimeImage 跳过拉取（仅供测试装配）。
	imagePullCoord *imagecoord.Coordinator
	// nodeDockerProv 按 nodeID 返回指向目标节点 agent docker proxy 的 SDK 客户端。
	// nil 时 phasePullRuntimeImage 跳过拉取。
	nodeDockerProv NodeDockerProvider
}

// NewAppInitializeHandler 创建 handler。dirs / containers / starter 可传 nil，
// 此时 handler 跳过对应步骤（仅初始化 api_key 与状态推进），
// 便于在 docker proxy / agent 装配未就绪时仍能保留旧行为兜底。
func NewAppInitializeHandler(
	store AppInitializeStore,
	dirs AgentDirInitializer,
	containers ContainerCreator,
	starter ContainerStarter,
	factory NewAPIClientFactory,
	cfg AppInitializeConfig,
) *AppInitializeHandler {
	return &AppInitializeHandler{
		store:      store,
		dirs:       dirs,
		containers: containers,
		starter:    starter,
		factory:    factory,
		cfg:        cfg,
	}
}

// SetAppInputUploader 注入 apps/<id>/input/ 文件上传能力。
// 生产环境必须注入(hermes-agent-pull 切换后 oc-entrypoint 需读取 manifest + resources),
// nil 时 phasePrepare 内 WriteAppInput 调用会直接报错。
func (h *AppInitializeHandler) SetAppInputUploader(w AppInputUploader) {
	h.inputFiles = w
}

// SetKnowledgeReader 注入主副本知识库读取能力。
// 装配后 phasePrepare 在 WriteAppInput 之后会通过 writeKnowledgeIntoInput 把
// 组织 + 应用知识库主副本的全部文件原样递归上传到
// apps/<id>/input/resources/knowledge/{org,app}/<rel>;
// 镜像 oc-entrypoint 在容器启动时自行扫描该目录、按需 render 成 hermes skills。
// nil 时跳过知识库写入(仅保留旧测试装配兼容)。
func (h *AppInitializeHandler) SetKnowledgeReader(r KnowledgeReader) {
	h.knowledge = r
}

// SetImagePullCoord 注入镜像拉取协调器。
// 生产环境必须注入，否则 phasePullRuntimeImage 会直接跳过拉取步骤。
func (h *AppInitializeHandler) SetImagePullCoord(coord *imagecoord.Coordinator) {
	h.imagePullCoord = coord
}

// SetNodeDockerProvider 注入 per-node Docker SDK 客户端工厂。
// 生产环境必须注入，否则 phasePullRuntimeImage 会直接跳过拉取步骤。
func (h *AppInitializeHandler) SetNodeDockerProvider(prov NodeDockerProvider) {
	h.nodeDockerProv = prov
}

// Handle 是 worker 调用入口。
// 5 阶段串行推进:每阶段进入前先校验状态机转移合法,跑实际工作前查幂等,
// 任何失败收敛到 status=error 并写入 last_error_status 记录来源阶段。
func (h *AppInitializeHandler) Handle(ctx context.Context, job sqlc.Job) error {
	if job.Type != domain.JobTypeAppInitialize {
		return fmt.Errorf("非 app_initialize 任务: %s", job.Type)
	}
	payload, err := decodePayload(job.PayloadJson)
	if err != nil {
		return err
	}
	appUUID, err := parseUUID(payload.AppID)
	if err != nil {
		return fmt.Errorf("非法 app_id: %w", err)
	}
	app, err := h.store.GetApp(ctx, appUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("应用 %s 不存在", payload.AppID)
		}
		return fmt.Errorf("查询应用失败: %w", err)
	}
	// 已离开初始化阶段直接成功(原本的幂等保留)。
	// binding_waiting 分支再做一次「渠道已绑定」自愈：上一次切换助手版本+重启
	// 触发的镜像重建在 transitionTo 阶段已经把行推到 binding_waiting，但若此时
	// channel_bindings 已是 bound（凭证保留在 bind mount，hermes 容器重启后无需
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
	version, err := h.store.GetAssistantVersion(ctx, app.VersionID)
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

	// 5 阶段定义:每阶段先 transitionTo 推 status,再 run 跑实际工作。
	// run 内部已根据 app 当前实际状态做幂等检查,允许重启后从中间阶段重入。
	// version 与 imageRef 通过闭包注入各阶段，避免在 handler 结构体上存储
	// 每次 Handle 调用的私有状态（防止并发安全问题）。
	steps := []struct {
		phase string
		run   func(context.Context, *sqlc.App, appInitializePayload, *progressReporter) error
	}{
		{domain.AppStatusPullingRuntimeImage, func(ctx context.Context, app *sqlc.App, p appInitializePayload, r *progressReporter) error {
			return h.phasePullRuntimeImage(ctx, app, p, r, imageRef)
		}},
		{domain.AppStatusPreparingRuntime, func(ctx context.Context, app *sqlc.App, p appInitializePayload, r *progressReporter) error {
			return h.phasePrepare(ctx, app, p, r, version)
		}},
		{domain.AppStatusCreatingContainer, func(ctx context.Context, app *sqlc.App, p appInitializePayload, r *progressReporter) error {
			return h.phaseCreate(ctx, app, p, r, imageRef)
		}},
		{domain.AppStatusStarting, h.phaseStart},
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
	// 写库失败时与 Handle 其余步骤一致走 markFailed：把 app 收敛到 status=error 并
	// 记录 last_error_status，避免行卡在 binding_waiting 而 worker 又把 job 当失败处理。
	if _, err := h.store.SetAppAppliedVersion(ctx, sqlc.SetAppAppliedVersionParams{
		ID:                     app.ID,
		AppliedVersionRevision: version.Revision,
		AppliedImageRef:        imageRef,
	}); err != nil {
		return h.markFailed(ctx, &app, domain.AppStatusBindingWaiting, fmt.Errorf("记录已应用版本信息失败: %w", err))
	}

	// 镜像重建场景下，channel_bindings 上一次的 bound 状态不会被清空，凭证又落在
	// bind mount 目录被新容器复用，无需用户重新扫码。若发现已 bound 就直接续推
	// 到 running，让概览页与渠道页状态一致——否则会出现「渠道页 bound、概览页
	// 待绑定」的卡死视图（finalizeChannelBound 只在 PollAuth 返回 bound 的边沿
	// 触发推进，不会被周期性兜底）。
	if err := h.promoteIfChannelBound(ctx, &app); err != nil {
		return h.markFailed(ctx, &app, domain.AppStatusBindingWaiting, err)
	}

	return h.writeInitAuditLog(ctx, app, job, payload)
}

// promoteIfChannelBound 在 status=binding_waiting 时探测该 app 是否已有 bound 渠道，
// 若有则把 status 推到 running。
//
// 触发场景：切换助手版本 + 重启 → 镜像变更走 app_runtime_ops 重建分支 → 入队新的
// app_initialize → 重建容器后走到 binding_waiting。整个流程不会重置渠道行，凭证又
// 在 bind mount 目录里持续可用，所以最终态应当是 running 而不是 binding_waiting。
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
	updated, err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusRunning})
	if err != nil {
		return fmt.Errorf("推进应用状态到 running 失败: %w", err)
	}
	*app = updated
	return nil
}

// transitionTo 推 status 并清空 progress_*;违反状态机直接返回 error,
// 由调用方决定是否 markFailed。
func (h *AppInitializeHandler) transitionTo(ctx context.Context, app *sqlc.App, to string, reporter *progressReporter) error {
	if app.Status == to {
		// 重启重入时已经处于目标阶段,跳过一次写库
		return nil
	}
	if err := domain.EnsureAppTransition(app.Status, to); err != nil {
		return err
	}
	updated, err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: to})
	if err != nil {
		return fmt.Errorf("写入应用状态失败: %w", err)
	}
	*app = updated
	reporter.FlushReset(ctx)
	return nil
}

// markFailed 把 status 推到 error,同时写入来源 phase 与错误文本，
// 让前端能展示"在哪一步失败"和"为什么失败"两层信息。
// 即便写库失败也返回原 cause,避免吞掉真实错误。
func (h *AppInitializeHandler) markFailed(ctx context.Context, app *sqlc.App, phase string, cause error) error {
	if _, err := h.store.MarkAppFailed(ctx, sqlc.MarkAppFailedParams{
		ID:               app.ID,
		LastErrorStatus:  pgtype.Text{String: phase, Valid: true},
		LastErrorMessage: pgtype.Text{String: cause.Error(), Valid: true},
	}); err != nil {
		return fmt.Errorf("%w (写入失败状态也失败: %v)", cause, err)
	}
	return cause
}

// pullRuntimeImageTimeout 是单次运行时镜像拉取的总时长上限。
//
// 取值 6 小时：运行时镜像体积大，且节点常经阿里云 ACR 个人版等带宽受限链路拉取，
// 慢网络下数小时才拉完属正常，上限必须足够大以免误杀正常拉取；同时又能保证
// 拉取流彻底卡死时最终冒泡成 error，让实例从 pulling_runtime_image 恢复为可重试
// 状态，而不是永久卡死（详见 phasePullRuntimeImage 内注释）。
const pullRuntimeImageTimeout = 6 * time.Hour

// phasePullRuntimeImage 通过 agent docker proxy 在目标节点拉取 hermes runtime 镜像。
//
// 流程：
//  1. 若 imagePullCoord 或 nodeDockerProv 未注入（如测试装配），直接跳过。
//  2. 使用调用方传入的 imageRef（由版本配置解析），通过 nodeDockerProv 获取目标节点的 Docker 客户端。
//  3. 调 imagePullCoord.PullImageOnNode：跨 manager 实例 single-flight，
//     同 (nodeID, imageRef) 串行，订阅者 chan 接收 NDJSON 进度并转发给 reporter。
//  4. 把 imageRef 和 sha256 写入 apps.runtime_image_ref / runtime_image_sha256。
func (h *AppInitializeHandler) phasePullRuntimeImage(ctx context.Context, app *sqlc.App, payload appInitializePayload, reporter *progressReporter, imageRef string) error {
	if h.imagePullCoord == nil || h.nodeDockerProv == nil {
		return nil
	}
	if payload.RuntimeNodeID == "" {
		return nil
	}
	cli, err := h.nodeDockerProv.DockerClientForNode(ctx, payload.RuntimeNodeID)
	if err != nil {
		return fmt.Errorf("获取节点 Docker 客户端失败: %w", err)
	}

	// subscriber 缓冲足以吸收一次 tick 积压，防止 coordinator 因满 chan 丢事件。
	subscriber := make(chan imagecoord.ProgressEvent, 8)
	// 启 goroutine 把进度转发给 reporter；PullImageOnNode 返回时 subscriber 已被 close。
	progressDone := make(chan struct{})
	go func() {
		defer close(progressDone)
		for ev := range subscriber {
			reporter.Receive(ctx, ev)
		}
	}()

	// 给整次镜像拉取套一个总时长上限。streaming docker client 无 http.Client.Timeout、
	// worker 也不给 job ctx 设 deadline，若 agent docker proxy 的 NDJSON 流半开卡死
	// （不来数据也不 EOF），doPullOnNode 会永久阻塞，实例永远停在 pulling_runtime_image：
	// progressReporter 每秒刷新 updated_at 让 reaper 的孤儿检测失明，前端 / 后端又都
	// 拒绝从该状态重试，最终无法恢复。这里的 deadline 是唯一兜底——命中后 doPullOnNode
	// 经 ctx.Done() 返回，失败冒泡至 markFailed，实例落到 error 后可重新初始化。
	pullCtx, cancelPull := context.WithTimeout(ctx, pullRuntimeImageTimeout)
	defer cancelPull()

	sha256, err := h.imagePullCoord.PullImageOnNode(pullCtx, payload.RuntimeNodeID, imageRef, cli, subscriber)
	<-progressDone // 等进度 goroutine 结束
	if err != nil {
		return fmt.Errorf("在节点 %s 拉取镜像 %s 失败: %w", payload.RuntimeNodeID, imageRef, err)
	}

	// 把镜像信息写入 apps 表，供后续创建容器和前端展示使用。
	updated, err := h.store.UpdateAppRuntimeImage(ctx, sqlc.UpdateAppRuntimeImageParams{
		ID:                 app.ID,
		RuntimeImageRef:    imageRef,
		RuntimeImageSha256: sha256,
	})
	if err != nil {
		return fmt.Errorf("写入 runtime 镜像信息失败: %w", err)
	}
	*app = updated
	return nil
}

// phasePrepare:在节点 agent 上准备目录、确保 api_key、上传 hermes 输入文件。
// 三段都已有局部幂等(InitAppDirs 覆盖写、ensureAPIKey 跳过 active、文件覆盖写),
// 重启重入直接跑安全。version 由 Handle 从 DB 加载后通过参数传入，避免重复查询。
func (h *AppInitializeHandler) phasePrepare(ctx context.Context, app *sqlc.App, payload appInitializePayload, _ *progressReporter, version sqlc.AssistantVersion) error {
	org, err := h.store.GetOrganization(ctx, app.OrgID)
	if err != nil {
		return fmt.Errorf("查询组织失败: %w", err)
	}
	owner, err := h.store.GetUser(ctx, app.OwnerUserID)
	if err != nil {
		return fmt.Errorf("查询应用 owner 失败: %w", err)
	}
	if h.dirs != nil && payload.RuntimeNodeID != "" {
		if err := h.dirs.InitAppDirs(ctx, payload.RuntimeNodeID, payload.AppID); err != nil {
			return fmt.Errorf("初始化节点应用目录失败: %w", err)
		}
	}
	containerAPIKey, err := h.ensureAPIKey(ctx, app)
	if err != nil {
		return err
	}
	if payload.RuntimeNodeID != "" {
		if err := h.writeAppInput(ctx, payload.RuntimeNodeID, *app, org, owner, containerAPIKey, version); err != nil {
			return err
		}
	}
	return nil
}

// phaseCreate:container_id 已存在则跳过(原 :284 行的幂等检查保留);否则
// 走原 ContainerCreator.CreateContainer 流程,把 ID/Name 写库。
//
// imageRef 由 Handle 经 ResolveRuntimeImage 解析后通过闭包传入，与
// phasePullRuntimeImage 收到的是同一值，是容器镜像的唯一来源。
func (h *AppInitializeHandler) phaseCreate(ctx context.Context, app *sqlc.App, payload appInitializePayload, _ *progressReporter, imageRef string) error {
	if app.ContainerID.String != "" {
		return nil
	}
	if h.containers == nil || payload.RuntimeNodeID == "" {
		return nil
	}
	node, err := h.store.GetRuntimeNode(ctx, app.RuntimeNodeID)
	if err != nil {
		return fmt.Errorf("查询 runtime node 失败: %w", err)
	}
	nodeDataRoot := node.NodeDataRoot.String
	if nodeDataRoot == "" {
		nodeDataRoot = "/var/lib/oc-agent"
	}
	// 容器挂载布局采用 input(ro) + data(rw) 双挂载：
	//   - apps/<id>/input → /opt/oc-input (ro)：oc-entrypoint 读 manifest.yaml +
	//     resources/*.md 渲染配置；只读避免容器内意外篡改输入清单。
	//   - apps/<id>/data  → /opt/data       (rw)：hermes 运行期数据 (workspace、
	//     生成的 config.yaml、sqlite、日志等) 的可写卷。
	// OPENAI_API_KEY / OPENAI_BASE_URL 不再通过 docker Env 注入：业务配置已经
	// 统一进 manifest.yaml → oc-entrypoint 渲染 config.yaml，避免双路注入语义漂移。
	spec := runtimepkg.ContainerSpec{
		Name:          "hermes-" + payload.AppID,
		Image:         imageRef,
		Networks:      h.cfg.ContainerNetworks,
		WorkingDir:    "/opt/data/workspace",
		RestartPolicy: "always",
		Volumes: []runtimepkg.VolumeMount{
			{
				HostPath:      filepath.Join(nodeDataRoot, "apps", payload.AppID, "input"),
				ContainerPath: "/opt/oc-input",
				ReadOnly:      true,
			},
			{
				HostPath:      filepath.Join(nodeDataRoot, "apps", payload.AppID, "data"),
				ContainerPath: "/opt/data",
			},
		},
	}
	info, err := h.containers.CreateContainer(ctx, payload.RuntimeNodeID, spec)
	if err != nil {
		return fmt.Errorf("创建容器失败: %w", err)
	}
	updated, err := h.store.SetAppContainer(ctx, sqlc.SetAppContainerParams{
		ID:            app.ID,
		ContainerID:   pgtype.Text{String: info.ID, Valid: info.ID != ""},
		ContainerName: pgtype.Text{String: info.Name, Valid: info.Name != ""},
	})
	if err != nil {
		return fmt.Errorf("写入 container_id 失败: %w", err)
	}
	*app = updated
	return nil
}

// phaseStart:启动容器并等健康检查。先 InspectContainer 看 State 做幂等;
// running 直接进健康检查;exited / created 才 Start。
func (h *AppInitializeHandler) phaseStart(ctx context.Context, app *sqlc.App, payload appInitializePayload, _ *progressReporter) error {
	if h.starter == nil || app.ContainerID.String == "" {
		return nil
	}
	containerID := app.ContainerID.String
	// inspector 实现可选;不实现时直接 Start(原行为)。
	state, ok := h.tryInspect(ctx, payload.RuntimeNodeID, containerID)
	if !ok || !state.Running {
		if err := h.starter.StartContainer(ctx, payload.RuntimeNodeID, containerID); err != nil {
			return fmt.Errorf("启动容器失败: %w", err)
		}
	}
	if checker, ok := h.starter.(HermesHealthChecker); ok {
		if err := checker.WaitContainerHealthy(ctx, payload.RuntimeNodeID, containerID, 120*time.Second); err != nil {
			return fmt.Errorf("等待 Hermes 容器健康失败: %w", err)
		}
	}
	return nil
}

// tryInspect 类型断言探测可选 InspectContainer 能力,未实现时返回 (zero, false)。
// 这样既支持新 adapter(实现 InspectContainer 后避免重复 Start),
// 也兼容旧 adapter(直接 Start,与原行为一致)。
func (h *AppInitializeHandler) tryInspect(ctx context.Context, nodeID, containerID string) (ContainerState, bool) {
	type inspector interface {
		InspectContainer(ctx context.Context, nodeID, containerID string) (ContainerState, error)
	}
	insp, ok := h.starter.(inspector)
	if !ok {
		return ContainerState{}, false
	}
	state, err := insp.InspectContainer(ctx, nodeID, containerID)
	if err != nil {
		return ContainerState{}, false
	}
	return state, true
}

// writeInitAuditLog 把原 Handle 末尾的审计日志逻辑独立出来,Handle 完成 binding_waiting
// 转移后调用一次。
func (h *AppInitializeHandler) writeInitAuditLog(ctx context.Context, app sqlc.App, job sqlc.Job, payload appInitializePayload) error {
	auditMetadata, err := json.Marshal(map[string]any{
		"job_id":       uuidToString(job.ID),
		"runtime_node": payload.RuntimeNodeID,
		"container_id": textOrEmpty(app.ContainerID),
	})
	if err != nil {
		return fmt.Errorf("序列化应用初始化审计元数据失败: %w", err)
	}
	if _, err := h.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
		ActorRole:    "system",
		OrgID:        app.OrgID,
		TargetType:   "app",
		TargetID:     uuidToString(app.ID),
		Action:       "initialize",
		Result:       "succeeded",
		MetadataJson: auditMetadata,
		// 不填 DetailMessage：initialize 的资源列已展示 app 名，详情列冗余。
	}); err != nil {
		return fmt.Errorf("写入应用初始化审计日志失败: %w", err)
	}
	return nil
}

// writeAppInput 通过 AppInputUploader 把应用初始化所需的 manifest.yaml + resources/*.md
// + 版本 skill tar + 组织/应用知识库主副本写到目标节点 apps/<id>/input/ 目录。
// 容器内 oc-entrypoint 读取该目录, 在镜像启动时把这些输入翻译成 hermes 自有 schema。
//
// version 参数提供版本级别的模型/路由/提示词/skill 信息，由 Handle 从 DB 加载后传入。
//
// containerAPIKey 必须是 ensureAPIKey 返回的真实 sk- token;调用方保证此函数
// 在 ensureAPIKey 之后执行, 避免写入占位符导致 hermes 调 new-api 返回 401。
// inputFiles 为 nil 时直接报错: hermes 容器必须有这些输入文件才能 bootstrap。
func (h *AppInitializeHandler) writeAppInput(ctx context.Context, nodeID string, app sqlc.App, org sqlc.Organization, owner sqlc.User, containerAPIKey string, version sqlc.AssistantVersion) error {
	if h.inputFiles == nil {
		return fmt.Errorf("AppInputUploader 未注入, hermes 容器无法 bootstrap")
	}
	appID := uuidToString(app.ID)

	// 通过共用的 AssembleVersionInputData 装配版本数据：推 skill tar + 解析 routing，
	// 与 restart 链路共享同一逻辑，避免两条链路写出的 manifest 版本字段漂移。
	versionData, err := AssembleVersionInputData(ctx, version, app, nodeID, h.cfg.SkillBlobs, h.inputFiles)
	if err != nil {
		return err
	}

	// adapter 绑定 nodeID, 让 hermes.WriteAppInput 的 (ctx, appID, relPath, body)
	// 接口形态在内部转发到 inputFiles.UploadAppInputFile (带 nodeID)。
	writer := &appInputUploadAdapter{up: h.inputFiles, nodeID: nodeID}
	in := BuildAppInputData(app, org, owner, containerAPIKey, versionData, AppInputBuildOptions{
		PlatformPrompt: h.cfg.PlatformPrompt,
		NewAPIBaseURL:  h.cfg.NewAPIBaseURL,
		DefaultModel:   h.cfg.LLM.DefaultModel,
	})
	if err := hermes.WriteAppInput(ctx, writer, appID, in); err != nil {
		return fmt.Errorf("写入应用 input 资源失败: %w", err)
	}

	// 把组织 / 应用知识库主副本原样递归推送到 input/resources/knowledge/{org,app}/。
	// 由镜像内 oc-entrypoint 在容器启动时按需 render 成 hermes 自身 skill;
	// manager 不再生成 SKILL.md, 解耦 hermes skill schema 演进。
	if err := h.writeKnowledgeIntoInput(ctx, nodeID, appID, uuidToString(app.OrgID)); err != nil {
		return err
	}
	return nil
}

// versionSkillMeta 是解析 skills_json 时使用的局部结构，只取 pushVersionSkills 所需字段。
// 与 service.AssistantVersionSkill 语义相同，局部定义避免 handlers 包反向依赖 service 包。
type versionSkillMeta struct {
	Name     string `json:"name"`
	FilePath string `json:"file_path"`
}

// pushVersionSkills 把版本 skill tar 推送到节点 apps/<appID>/input/resources/skills/<name>.tar，
// 返回写入 manifest.resources.skills 的相对路径列表。
// skillBlobs 或 uploader 为 nil（测试装配/无 skill 配置）时跳过推送，返回空列表。
func pushVersionSkills(ctx context.Context, skillBlobs SkillBlobReader, uploader AppInputUploader, nodeID, appID string, skillsJson []byte) ([]string, error) {
	if skillBlobs == nil || uploader == nil || len(skillsJson) == 0 {
		return nil, nil
	}
	var skills []versionSkillMeta
	if err := json.Unmarshal(skillsJson, &skills); err != nil {
		return nil, fmt.Errorf("解析版本 skills_json 失败: %w", err)
	}
	relPaths := make([]string, 0, len(skills))
	for _, skill := range skills {
		// skill.Name 来自版本 skills_json，会被拼进上传相对路径；为防路径穿越，
		// 拒绝空名或含 '/'、'\\'、'..' 的名称。
		if skill.Name == "" || strings.ContainsAny(skill.Name, "/\\") || strings.Contains(skill.Name, "..") {
			return nil, fmt.Errorf("非法 skill 名称 %q", skill.Name)
		}
		rc, err := skillBlobs.OpenSkill(skill.FilePath)
		if err != nil {
			return nil, fmt.Errorf("打开 skill %s 主副本失败: %w", skill.Name, err)
		}
		body, readErr := io.ReadAll(rc)
		_ = rc.Close()
		if readErr != nil {
			return nil, fmt.Errorf("读取 skill %s 主副本失败: %w", skill.Name, readErr)
		}
		relPath := "resources/skills/" + skill.Name + ".tar"
		if err := uploader.UploadAppInputFile(ctx, nodeID, appID, relPath, bytes.NewReader(body)); err != nil {
			return nil, fmt.Errorf("上传 skill %s 失败: %w", skill.Name, err)
		}
		relPaths = append(relPaths, relPath)
	}
	return relPaths, nil
}

// AssembleVersionInputData 把 AssistantVersion 装配成 BuildAppInputData 所需的 AppInputVersionData：
// 解析 routing_json，并把版本 skill tar 经 uploader 推送到节点 input/resources/skills/。
// init(writeAppInput) 与 restart(appInputRefresher) 两条链路共用此函数，确保两边写出的
// manifest 版本数据完全一致，避免「初始化看得见、restart 后字段漂移」。
func AssembleVersionInputData(ctx context.Context, version sqlc.AssistantVersion, app sqlc.App, nodeID string, skillBlobs SkillBlobReader, uploader AppInputUploader) (AppInputVersionData, error) {
	appID := uuidToString(app.ID)
	skillRelPaths, err := pushVersionSkills(ctx, skillBlobs, uploader, nodeID, appID, version.SkillsJson)
	if err != nil {
		return AppInputVersionData{}, err
	}
	var routing map[string]string
	if len(version.RoutingJson) > 0 {
		if err := json.Unmarshal(version.RoutingJson, &routing); err != nil {
			return AppInputVersionData{}, fmt.Errorf("解析版本 routing_json 失败: %w", err)
		}
	}
	return AppInputVersionData{
		MainModel:     version.MainModel,
		Routing:       routing,
		SystemPrompt:  version.SystemPrompt,
		SkillRelPaths: skillRelPaths,
	}, nil
}

// AppInputVersionData 是 BuildAppInputData 所需的「实例绑定版本」业务数据。
// 由 Handle 从 DB 加载 AssistantVersion 后解析，再传给 BuildAppInputData。
type AppInputVersionData struct {
	// MainModel 版本主模型 → manifest.app.model；空时退回 opts.DefaultModel，再退回 "default"。
	MainModel string
	// Routing 版本智能路由 → manifest.routing；空 map 时 omitempty 省略。
	Routing map[string]string
	// SystemPrompt 版本内置提示词 → resources/persona.md。
	SystemPrompt string
	// SkillRelPaths 已推送到 input/ 的版本 skill tar 相对路径 → manifest.resources.skills。
	SkillRelPaths []string
}

// AppInputBuildOptions 是 BuildAppInputData 的非 DB / 非版本来源参数集合。
//
// 单独抽成结构体而不是逐个传, 让 init handler 与 restart 阶段的 wiring 端
// refresher 都能从同一份 AppInitializeConfig 里取出对应字段, 避免漏传一个
// 参数导致两边语义偏移。
type AppInputBuildOptions struct {
	// PlatformPrompt 写入 resources/platform-rules.md, 由配置文件
	// hermes.system_prompt_template 提供, 容器内 hermes 按需引用。
	PlatformPrompt string
	// NewAPIBaseURL 写入 manifest.credentials.openai.base_url;
	// 容器内 hermes 会再拼 "/v1" 后调 new-api。空串时回退到默认值
	// "http://new-api:3000" (与老 config.yaml 兜底一致)。
	NewAPIBaseURL string
	// DefaultModel 是 version.MainModel 为空时的兜底模型名。两者都为空时
	// 写入 "default" 占位, 避免 manifest.app.model 落空串。
	DefaultModel string
}

// BuildAppInputData 把 DB 行 + 版本数据 + 解密后的 api key 装配成 hermes.AppInputData。
//
// init 与 restart 两条链路共享同一份字段映射, 确保「初始化写入的 manifest」
// 与「restart 前重写的 manifest」字段语义完全一致——这是 hermes-image-self-init
// 流程的核心约束: oc-entrypoint 每次启动都重渲染 config.yaml / SOUL.md,
// 输入字段语义偏移会直接表现为线上"改模型不生效"或"老 prompt 残留"。
//
// 字段策略:
//   - model: 优先 version.MainModel, 其次 opts.DefaultModel, 最后写 "default" 占位;
//   - base_url: 优先 opts.NewAPIBaseURL, 兜底 "http://new-api:3000";
//   - persona: 来自 version.SystemPrompt，占位符渲染由 hermes.WriteAppInput 统一处理;
//   - routing / skills: 直接透传版本数据，hermes manifest v2 格式。
//
// containerAPIKey 必须是真实 sk- 明文; 调用方负责从 ciphertext 解密。
// 该函数纯装配, 不做 IO, 因此无 ctx 参数, 也不返回 error。
func BuildAppInputData(app sqlc.App, org sqlc.Organization, owner sqlc.User, containerAPIKey string, version AppInputVersionData, opts AppInputBuildOptions) hermes.AppInputData {
	model := version.MainModel
	if model == "" {
		model = opts.DefaultModel
	}
	if model == "" {
		model = "default"
	}
	baseURL := opts.NewAPIBaseURL
	if baseURL == "" {
		baseURL = "http://new-api:3000"
	}
	return hermes.AppInputData{
		AppID:         uuidToString(app.ID),
		AppName:       app.Name,
		Model:         model,
		OpenAIAPIKey:  containerAPIKey,
		OpenAIBaseURL: baseURL,
		PersonaText:   version.SystemPrompt,
		PlatformRule:  opts.PlatformPrompt,
		Routing:       version.Routing,
		SkillRelPaths: version.SkillRelPaths,
		OrgName:       org.Name,
		OwnerName:     owner.DisplayName,
	}
}

// writeKnowledgeIntoInput 递归把组织 + 应用主副本知识库写到
// apps/<appID>/input/resources/knowledge/{org,app}/<相对路径>。
//
// 顺序: 先 org 再 app, 与 KnowledgeReader 主副本路径
// (org/<orgID>/knowledge/* 与 org/<orgID>/app/<appID>/knowledge/*) 自然对应;
// 写入顺序对优先级无影响 (优先级由镜像 oc-entrypoint 自行解析)。
//
// h.knowledge 为 nil (测试装配 / 旧 wiring) 时直接跳过; 任一文件读写失败
// 立即冒泡, 由 worker 决定是否重试。
func (h *AppInitializeHandler) writeKnowledgeIntoInput(ctx context.Context, nodeID, appID, orgID string) error {
	if h.knowledge == nil {
		return nil
	}
	// scopes 列表显式列出两类 source/target 映射, 避免在循环里硬编码字符串拼接,
	// 让"主副本前缀 → input 子目录"的对应关系一目了然。
	scopes := []struct {
		// scopeName 决定写到 resources/knowledge/<scopeName>/ 下;
		// 与镜像 oc-entrypoint 约定的 org / app 命名严格一致。
		scopeName string
		// prefix 是 KnowledgeReader 主副本的前缀路径, 与 KnowledgeService 写入约定一致。
		prefix string
	}{
		{scopeName: "org", prefix: fmt.Sprintf("org/%s/knowledge", orgID)},
		{scopeName: "app", prefix: fmt.Sprintf("org/%s/app/%s/knowledge", orgID, appID)},
	}
	for _, s := range scopes {
		prefix := s.prefix
		scope := s.scopeName
		err := h.knowledge.WalkFiles(prefix, func(relPath string, _ int64) error {
			master := prefix + "/" + relPath
			reader, _, err := h.knowledge.Open(master)
			if err != nil {
				return fmt.Errorf("打开主副本 %s 失败: %w", master, err)
			}
			body, readErr := io.ReadAll(reader)
			_ = reader.Close()
			if readErr != nil {
				return fmt.Errorf("读取主副本 %s 失败: %w", master, readErr)
			}
			target := fmt.Sprintf("resources/knowledge/%s/%s", scope, relPath)
			if err := h.inputFiles.UploadAppInputFile(ctx, nodeID, appID, target, strings.NewReader(string(body))); err != nil {
				return fmt.Errorf("上传知识库 %s 失败: %w", target, err)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("写入 %s 知识库失败: %w", scope, err)
		}
	}
	return nil
}

// appInputUploadAdapter 把 handler 持有的 AppInputUploader (带 nodeID 的 3+1 参数
// 上传方法) 适配成 hermes.AppInputWriter (固定 appID 维度的 WriteAppInputFile)。
//
// hermes 包面向单 app 维度做编排, 不关心 nodeID; manager 这边一个 handler 可能
// 同时服务多节点, 因此 wiring 注入的是按 nodeID 分发的 uploader。adapter 在
// handler 内部为每个 app 实例化一次, 绑定其 RuntimeNodeID 后即满足 hermes 接口形态。
type appInputUploadAdapter struct {
	// up 是底层按 nodeID 路由的上传能力 (生产实现: AgentBackedAdapter.UploadAppInputFile)。
	up AppInputUploader
	// nodeID 在 handler 取出 payload.RuntimeNodeID 后绑定; 整个 WriteAppInput 调用
	// 期间不变, 保证多文件上传都落到同一节点。
	nodeID string
}

// WriteAppInputFile 实现 hermes.AppInputWriter。
// 参数顺序与 hermes 包接口对齐 (ctx, appID, relPath, body); 内部追加 nodeID 后转发,
// 不附加业务校验 (路径越界 / 沙箱由 agent 端统一拒绝)。
func (a *appInputUploadAdapter) WriteAppInputFile(ctx context.Context, appID, relPath string, body io.Reader) error {
	return a.up.UploadAppInputFile(ctx, a.nodeID, appID, relPath, body)
}

// NewAppInputUploadAdapter 把 nodeID 与 AppInputUploader 绑定成
// hermes.AppInputWriter, 供 wiring 层 (cmd/server) 在 restart 链路装配
// AppInputRefresher 时复用; init handler 内部仍用未导出的 appInputUploadAdapter。
// 暴露这个构造器避免在 wiring 处重复定义同样的两参数 wrapper 类型,
// 也保证 init 与 restart 两条链路走同一适配实现。
func NewAppInputUploadAdapter(up AppInputUploader, nodeID string) hermes.AppInputWriter {
	return &appInputUploadAdapter{up: up, nodeID: nodeID}
}

// ensureAPIKey 走「以组织业务 user 身份创 token + 拉完整 sk-」流程，加密落库后返回明文 sk-。
//
// 已经 active 的应用直接读 ciphertext 解密返回，避免重复创建。
// 解密失败 / 拉 key 失败都直接报错；不再有"全局 fallback sk-"的兜底路径。
func (h *AppInitializeHandler) ensureAPIKey(ctx context.Context, app *sqlc.App) (string, error) {
	if app.ApiKeyStatus == domain.APIKeyStatusActive {
		if !app.NewapiKeyCiphertext.Valid || app.NewapiKeyCiphertext.String == "" {
			return "", fmt.Errorf("应用 %s 已 active 但 newapi_key_ciphertext 为空", uuidToString(app.ID))
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
	keyName := fmt.Sprintf("app-%s", uuidToString(app.ID))
	key, err := client.CreateAPIKey(ctx, newapi.CreateAPIKeyInput{
		Name:       keyName,
		Models:     []string{},
		UnlimitedQ: true,
	})
	if err != nil {
		if h.cfg.AuditHelper != nil {
			h.cfg.AuditHelper.RecordFailure(ctx, audit.NewAPIFailureContext{
				OrgID:    uuidToString(app.OrgID),
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
				OrgID:    uuidToString(app.OrgID),
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
	updated, err := h.store.SetAppNewAPIKey(ctx, sqlc.SetAppNewAPIKeyParams{
		ID:                  app.ID,
		NewapiKeyID:         pgtype.Text{String: fmt.Sprintf("%d", key.ID), Valid: true},
		NewapiKeyCiphertext: pgtype.Text{String: ciphertext, Valid: true},
		ApiKeyStatus:        domain.APIKeyStatusActive,
		// 显式落 newapi_key_name：与上面 CreateAPIKey 用的 keyName 保持一致，
		// 让后续 usage 查询不必再次拼 "app-<uuid>"，直接从 apps 表读字段即可。
		NewapiKeyName: pgtype.Text{String: keyName, Valid: true},
	})
	if err != nil {
		return "", fmt.Errorf("写入 api_key 失败: %w", err)
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

// uuidToString 把 pgtype.UUID 安全转成 string，无效时返回空串。
func uuidToString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, b := range id.Bytes {
		out = append(out, digits[b>>4], digits[b&0x0f])
		if i == 3 || i == 5 || i == 7 || i == 9 {
			out = append(out, '-')
		}
	}
	return string(out)
}

type appInitializePayload struct {
	AppID         string `json:"app_id"`
	RuntimeNodeID string `json:"runtime_node"`
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

func parseUUID(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		return pgtype.UUID{}, err
	}
	return id, nil
}

func textOrEmpty(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}
