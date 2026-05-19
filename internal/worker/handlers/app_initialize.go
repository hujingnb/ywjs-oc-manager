package handlers

import (
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

	"oc-manager/internal/audit"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/hermes"
	"oc-manager/internal/integrations/newapi"
	dockerclient "github.com/docker/docker/client"

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

// AppRuntimeFileWriter 抽象在节点 agent 上写运行时配置文件的能力。
// 老 Hermes 链路(.hermes/SOUL.md/config.yaml/.env/skills/) 用此接口;
// 当前 AppInitializeHandler 已切到 AppInputUploader 走 input/ 路径,
// 该接口仅保留供 ChannelCheckBindingHandler 等老路径 handler 引用,
// T24/T25 切完所有调用方后整体下线。
type AppRuntimeFileWriter interface {
	UploadAppRuntimeFile(ctx context.Context, nodeID, appID, relPath string, content io.Reader) error
}

// AppInputUploader 抽象在节点 agent 上写应用输入文件 (apps/<id>/input/) 的能力。
// 由 internal/integrations/runtime.AgentBackedAdapter 实现 (内部转发到
// agent.AgentFileClient.UploadAppInputFile, 命中 agent T13 新增的 input/file 路由)。
//
// 与老 AppRuntimeFileWriter 的差别:写的不再是 hermes 内部的 SOUL.md/config.yaml/.env,
// 而是 manifest.yaml + resources/*.md 这一层「容器外可读的输入资源」;镜像 oc-entrypoint
// 在容器启动时再把它们翻译成 hermes 自有 schema。
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
	RuntimeImage         string
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
	if cfg.RuntimeImage == "" {
		cfg.RuntimeImage = "hermes-runtime:dev"
	}
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
	// 已离开初始化阶段直接成功(原本的幂等保留)
	if app.Status == domain.AppStatusBindingWaiting || app.Status == domain.AppStatusRunning {
		return nil
	}

	reporter := newProgressReporter(app.ID, h.store)

	// 5 阶段定义:每阶段先 transitionTo 推 status,再 run 跑实际工作。
	// run 内部已根据 app 当前实际状态做幂等检查,允许重启后从中间阶段重入。
	steps := []struct {
		phase string
		run   func(context.Context, *sqlc.App, appInitializePayload, *progressReporter) error
	}{
		{domain.AppStatusPullingRuntimeImage, h.phasePullRuntimeImage},
		{domain.AppStatusPreparingRuntime, h.phasePrepare},
		{domain.AppStatusCreatingContainer, h.phaseCreate},
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
	return h.writeInitAuditLog(ctx, app, job, payload)
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

// phasePullRuntimeImage 通过 agent docker proxy 在目标节点拉取 hermes runtime 镜像。
//
// 流程：
//  1. 若 imagePullCoord 或 nodeDockerProv 未注入（如测试装配），直接跳过。
//  2. 从 cfg.RuntimeImage 取镜像引用，通过 nodeDockerProv 获取目标节点的 Docker 客户端。
//  3. 调 imagePullCoord.PullImageOnNode：跨 manager 实例 single-flight，
//     同 (nodeID, imageRef) 串行，订阅者 chan 接收 NDJSON 进度并转发给 reporter。
//  4. 把 imageRef 和 sha256 写入 apps.runtime_image_ref / runtime_image_sha256。
func (h *AppInitializeHandler) phasePullRuntimeImage(ctx context.Context, app *sqlc.App, payload appInitializePayload, reporter *progressReporter) error {
	if h.imagePullCoord == nil || h.nodeDockerProv == nil {
		return nil
	}
	if payload.RuntimeNodeID == "" {
		return nil
	}
	imageRef := h.cfg.RuntimeImage
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

	sha256, err := h.imagePullCoord.PullImageOnNode(ctx, payload.RuntimeNodeID, imageRef, cli, subscriber)
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
// 重启重入直接跑安全。
func (h *AppInitializeHandler) phasePrepare(ctx context.Context, app *sqlc.App, payload appInitializePayload, _ *progressReporter) error {
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
		if err := h.writeAppInput(ctx, payload.RuntimeNodeID, *app, org, owner, containerAPIKey); err != nil {
			return err
		}
	}
	return nil
}

// phaseCreate:container_id 已存在则跳过(原 :284 行的幂等检查保留);否则
// 走原 ContainerCreator.CreateContainer 流程,把 ID/Name 写库。
func (h *AppInitializeHandler) phaseCreate(ctx context.Context, app *sqlc.App, payload appInitializePayload, _ *progressReporter) error {
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
	// 优先使用 phasePullRuntimeImage 写入的 RuntimeImageRef；
	// 若未拉取（如 imagePullCoord 未注入），退回到 cfg.RuntimeImage。
	imageRef := app.RuntimeImageRef
	if imageRef == "" {
		imageRef = h.cfg.RuntimeImage
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

// writeAppInput 通过 AppInputUploader 把应用初始化所需的 manifest.yaml + 三层
// rules + persona + 组织/应用知识库主副本写到目标节点 apps/<id>/input/ 目录。
// 容器内 oc-entrypoint 读取该目录, 在镜像启动时把这些输入翻译成 hermes 自有 schema。
//
// 与老 writeHermesFiles 的差别:
//   - 不再写 .hermes/SOUL.md/config.yaml/.env/skills/ (这些由镜像内部生成);
//   - 写的是「人类可读 / 工具友好」的输入资源, 由 hermes.WriteAppInput 统一编排;
//   - 知识库不再渲染成 SKILL.md, 而是原样递归推送, 让镜像 oc-entrypoint
//     按需 render (避免 manager 与 hermes skill schema 强耦合)。
//
// containerAPIKey 必须是 ensureAPIKey 返回的真实 sk- token;调用方保证此函数
// 在 ensureAPIKey 之后执行, 避免写入占位符导致 hermes 调 new-api 返回 401。
// inputFiles 为 nil 时直接报错: hermes 容器必须有这些输入文件才能 bootstrap。
func (h *AppInitializeHandler) writeAppInput(ctx context.Context, nodeID string, app sqlc.App, org sqlc.Organization, owner sqlc.User, containerAPIKey string) error {
	if h.inputFiles == nil {
		return fmt.Errorf("AppInputUploader 未注入, hermes 容器无法 bootstrap")
	}
	appID := uuidToString(app.ID)

	// model: 优先使用 app.ModelID, 兜底用 cfg.LLM.DefaultModel; 都为空时
	// 落 "default" 占位 (与老 RenderConfigYAML 行为一致, 避免下游 nil-deref)。
	model := app.ModelID
	if model == "" {
		model = h.cfg.LLM.DefaultModel
	}
	if model == "" {
		model = "default"
	}
	// base_url: 写入 manifest.credentials.openai.base_url; 容器内 hermes 会
	// 再拼 "/v1" 后调 new-api, 与老 config.yaml 行为对齐。
	baseURL := h.cfg.NewAPIBaseURL
	if baseURL == "" {
		baseURL = "http://new-api:3000"
	}

	// adapter 绑定 nodeID, 让 hermes.WriteAppInput 的 (ctx, appID, relPath, body)
	// 接口形态在内部转发到 inputFiles.UploadAppInputFile (带 nodeID)。
	writer := &appInputUploadAdapter{up: h.inputFiles, nodeID: nodeID}
	in := hermes.AppInputData{
		AppID:            appID,
		AppName:          app.Name,
		Model:            model,
		OpenAIAPIKey:     containerAPIKey,
		OpenAIBaseURL:    baseURL,
		PersonaText:      h.cfg.SystemPromptTemplate,
		PlatformRule:     h.cfg.PlatformPrompt,
		OrganizationRule: "", // 组织层 rule 待后续 organization persona/rules 字段补齐
		ApplicationRule:  textOrEmpty(app.AppPrompt),
		OrgName:          org.Name,
		OwnerName:        owner.DisplayName,
	}
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
