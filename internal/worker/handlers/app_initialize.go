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
	runtimepkg "oc-manager/internal/integrations/runtime"
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
}

// ImageDistributor 抽象镜像分发能力。
type ImageDistributor interface {
	EnsureRuntimeImage(ctx context.Context, nodeID, image string) (any, error)
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
// Hermes 时代 manager 通过该接口把 SOUL.md / config.yaml / .env / skills/*/SKILL.md
// 上传到节点 dataRoot/apps/<appID>/.hermes/,确保多节点部署下 manager 与 docker
// daemon 不必同机。注入失败(nil)时 handler 直接报错,因为 Hermes 容器必须有
// 这些文件才能启动。
type AppRuntimeFileWriter interface {
	UploadAppRuntimeFile(ctx context.Context, nodeID, appID, relPath string, content io.Reader) error
}

// KnowledgeReader 抽象 manager 主副本的读能力,供 writeHermesFiles 在容器启动前
// 把组织/应用知识库批量渲染成 .hermes/skills/kb-*-<slug>/SKILL.md。
//
// WalkFiles 递归遍历 prefix(如 "org/<id>/knowledge")下所有普通文件,每个文件
// 回调一次,relPath 相对 prefix、统一 '/' 分隔。
// Open 打开主副本中的指定文件;调用方负责关闭。
//
// nil 装配时 writeSkillsFromKnowledge 直接跳过,使旧装配/测试装配仍可工作。
type KnowledgeReader interface {
	WalkFiles(prefix string, fn func(relPath string, size int64) error) error
	Open(masterPath string) (io.ReadCloser, int64, error)
}

// ContainerStarter 抽象创建后启动容器的能力（minimal 接口）。
// 与 app_runtime_ops.go 的 ContainerLifecycle 不重叠：那个接口要求 Start/Stop/Restart/Remove
// 四个方法，初始化阶段只需要 Start，因此独立小接口便于测试 mock。
type ContainerStarter interface {
	StartContainer(ctx context.Context, nodeID, containerID string) error
}

// HermesHealthChecker 是 ContainerStarter 的扩展：实现该接口的 starter 在容器启动后
// 等 docker HEALTHCHECK 报 healthy 才返回。AgentBackedAdapter 实现此接口（WaitContainerHealthy）。
// handler 通过类型断言探测，未实现的旧实现仍能跑通——
// 只是状态机会立即推到 binding_waiting，等后续健康检查任务再确认状态。
type HermesHealthChecker interface {
	WaitContainerHealthy(ctx context.Context, nodeID, containerID string, timeout time.Duration) error
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
// SystemPromptTemplate：Hermes SOUL.md 的平台层模板，{var} 占位符在渲染时展开；
// 不再使用 legacy OpenClaw 时代的 {{workspace_dir}} 格式。
//
// Cipher：把 new-api 返回的完整 sk- 加密后写入 apps.newapi_key_ciphertext，
// 全程不入日志。
//
// DataDir 字段已从 Hermes 文件分发路径移除：Hermes 配置文件现在通过
// AppRuntimeFileWriter.UploadAppRuntimeFile 上传到目标节点 agent，
// 不再写入 manager 本机目录。DataDir 保留供其他特定场景（如 workspaceService）使用。
type AppInitializeConfig struct {
	RuntimeImage         string
	PlatformPrompt       string
	SystemPromptTemplate string
	Cipher               *auth.Cipher
	// DataDir 是 manager 宿主机上的数据根目录，仅供其他特定场景使用。
	// Hermes 文件分发已走 UploadAppRuntimeFile，不再使用此字段。
	DataDir string
	// NewAPIBaseURL 是 new-api 内网访问 URL（不含 /v1），写入 Hermes config.yaml 与 .env。
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
//  3. 调 ImageDistributor 把 Hermes runtime 镜像同步到目标节点；
//  4. 调 AgentDirInitializer 在节点上准备 apps/<id>/ 目录；
//  5. api_key 不 active 时调 new-api 创建并 cipher.Encrypt 写库（ensureAPIKey）；
//  6. 渲染 SOUL.md/config.yaml/.env/skills/ 并通过 AppRuntimeFileWriter 上传到目标节点
//     agent 的 dataRoot/apps/<id>/.hermes/（使用步骤 5 的真实 token，避免 HTTP 401）；
//  7. container_id 为空时调 ContainerCreator.CreateContainer，把 ID/Name 写库；
//  8. 调 ContainerStarter.StartContainer 启动容器；
//  9. starter 实现 HermesHealthChecker 时等 docker HEALTHCHECK 报 healthy；
// 10. 推 status=binding_waiting，由 channel 流程接管后续。
//
// 任意一步失败立即冒泡，由 worker 重试或入 failed；状态机字段只在显式步骤里单独写。
type AppInitializeHandler struct {
	store        AppInitializeStore
	images       ImageDistributor
	dirs         AgentDirInitializer
	runtimeFiles AppRuntimeFileWriter
	knowledge    KnowledgeReader
	containers   ContainerCreator
	starter      ContainerStarter
	factory      NewAPIClientFactory
	cfg          AppInitializeConfig
}

// NewAppInitializeHandler 创建 handler。dirs / containers / starter 可传 nil，
// 此时 handler 跳过对应步骤（仅初始化 api_key 与状态推进），
// 便于在 docker proxy / agent 装配未就绪时仍能保留旧行为兜底。
func NewAppInitializeHandler(
	store AppInitializeStore,
	images ImageDistributor,
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
		images:     images,
		dirs:       dirs,
		containers: containers,
		starter:    starter,
		factory:    factory,
		cfg:        cfg,
	}
}

// SetRuntimeFileWriter 注入 Hermes runtime 配置文件上传能力。
// 生产环境必须注入（Hermes 容器启动前须有 SOUL.md/config.yaml/.env），
// nil 时 writeHermesFiles 直接返回错误。
func (h *AppInitializeHandler) SetRuntimeFileWriter(w AppRuntimeFileWriter) {
	h.runtimeFiles = w
}

// SetKnowledgeReader 注入主副本知识库读取能力。
// 装配后 writeHermesFiles 会在写完 SOUL.md/config.yaml/.env 后,
// 遍历组织/应用主副本目录,把每个文件渲染成 .hermes/skills/kb-*-<slug>/SKILL.md。
// nil 时跳过 skill 写入(仅保留旧测试装配兼容)。
func (h *AppInitializeHandler) SetKnowledgeReader(r KnowledgeReader) {
	h.knowledge = r
}

// Handle 是 worker 调用入口，签名匹配 handlers.HandlerFunc。
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
	if app.Status == domain.AppStatusRunning || app.Status == domain.AppStatusBindingWaiting {
		// 幂等：应用已经离开初始化阶段，重复执行直接成功。
		return nil
	}
	org, err := h.store.GetOrganization(ctx, app.OrgID)
	if err != nil {
		return fmt.Errorf("查询组织失败: %w", err)
	}
	owner, err := h.store.GetUser(ctx, app.OwnerUserID)
	if err != nil {
		return fmt.Errorf("查询应用 owner 失败: %w", err)
	}

	if h.images != nil && payload.RuntimeNodeID != "" {
		if _, err := h.images.EnsureRuntimeImage(ctx, payload.RuntimeNodeID, h.cfg.RuntimeImage); err != nil {
			return fmt.Errorf("分发 Hermes 镜像失败: %w", err)
		}
	}

	// 节点 agent 准备应用目录；Hermes 时代 .hermes/ 由 manager 本地写入，
	// InitAppDirs 仍保留以兼容存量部署。
	if h.dirs != nil && payload.RuntimeNodeID != "" {
		if err := h.dirs.InitAppDirs(ctx, payload.RuntimeNodeID, payload.AppID); err != nil {
			return fmt.Errorf("初始化节点应用目录失败: %w", err)
		}
	}

	// 先拿真实 containerAPIKey,再写 Hermes 配置文件,确保 config.yaml api_key 和
	// .env OPENAI_API_KEY 都是真实 token,避免 Hermes 用占位符调 new-api 返回 HTTP 401。
	containerAPIKey, err := h.ensureAPIKey(ctx, &app)
	if err != nil {
		return err
	}
	_ = org // org 已在上文用于 hermes 文件渲染；ensureAPIKey 现在通过 factory 自行获取组织凭据。

	// 通过 runtime-agent UploadAppRuntimeFile 把 Hermes 配置文件上传到目标节点；
	// 支持多节点部署（manager 与 docker daemon 可在不同节点）。
	// payload.RuntimeNodeID 为空时跳过（仅存在于旧测试装配）。
	if payload.RuntimeNodeID != "" {
		if err := h.writeHermesFiles(ctx, payload.RuntimeNodeID, app, org, owner, containerAPIKey); err != nil {
			return err
		}
	}

	if app.ContainerID.String == "" && h.containers != nil {
		node, err := h.store.GetRuntimeNode(ctx, app.RuntimeNodeID)
		if err != nil {
			return fmt.Errorf("查询 runtime node 失败: %w", err)
		}
		// Hermes 容器规格：单一 bind mount，把节点本地 .hermes/ 挂载到 /opt/data。
		// HostPath 使用节点 NodeDataRoot 拼接，而非 manager 本机路径，
		// 确保多节点部署下 bind mount 路径指向 docker daemon 所在节点的正确位置。
		nodeDataRoot := node.NodeDataRoot.String
		if nodeDataRoot == "" {
			nodeDataRoot = "/var/lib/oc-agent"
		}
		spec := runtimepkg.ContainerSpec{
			Name:     "hermes-" + payload.AppID,
			Image:    h.cfg.RuntimeImage,
			Networks: h.cfg.ContainerNetworks,
			// 把容器进程启动 cwd 设为 /opt/data/workspace,让 agent 默认在
			// workspace 子目录下执行 terminal / file 工具,生成的文件落在
			// manager workspace API 可读的物理位置(.hermes/workspace)。
			// agent init 已预建该目录,容器启动时无需额外 mkdir。
			WorkingDir: "/opt/data/workspace",
			Env: map[string]string{
				// Hermes 读取 /opt/data/.env 中的 OPENAI_API_KEY；
				// 此处额外传 env 作为启动时的直接覆盖，确保 token 立即可用。
				"OPENAI_API_KEY":  containerAPIKey,
				"OPENAI_BASE_URL": h.cfg.NewAPIBaseURL + "/v1",
			},
			Volumes: []runtimepkg.VolumeMount{
				// 单一挂载：将节点 dataRoot/apps/<id>/.hermes/ 映射到容器 /opt/data（Hermes 主目录）。
				// SOUL.md/config.yaml/.env/skills/ 均在此目录下，Hermes 启动时自动读取。
				// HostPath 为节点本地路径（由 runtime-agent 写入），而非 manager 本机路径。
				{HostPath: filepath.Join(nodeDataRoot, "apps", payload.AppID, ".hermes"), ContainerPath: "/opt/data"},
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
		app = updated
		// 启动容器；Hermes gateway 启动 + iLink 长轮询建立约 5-10s。
		if h.starter != nil && info.ID != "" {
			if err := h.starter.StartContainer(ctx, payload.RuntimeNodeID, info.ID); err != nil {
				return fmt.Errorf("启动容器失败: %w", err)
			}
			// starter 实现 HermesHealthChecker 时等 docker HEALTHCHECK 报 healthy，
			// 避免应用过早进入待绑定状态导致后续 channel_start_login 直接撞到 gateway 未就绪。
			// 未实现的旧 starter 跳过此步，状态机会直接推到 binding_waiting。
			if checker, ok := h.starter.(HermesHealthChecker); ok {
				// 留 120s 余量：Hermes 启动 + iLink 连接通常 5-10s，HEALTHCHECK start-period 60s。
				if err := checker.WaitContainerHealthy(ctx, payload.RuntimeNodeID, info.ID, 120*time.Second); err != nil {
					return fmt.Errorf("等待 Hermes 容器健康失败: %w", err)
				}
			}
		}
	}

	if app.Status != domain.AppStatusBindingWaiting {
		updated, err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{
			ID:     app.ID,
			Status: domain.AppStatusBindingWaiting,
		})
		if err != nil {
			return fmt.Errorf("更新应用状态失败: %w", err)
		}
		app = updated
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
		}); err != nil {
			return fmt.Errorf("写入应用初始化审计日志失败: %w", err)
		}
	}
	return nil
}

// writeHermesFiles 把 Hermes 运行时配置文件通过 runtime-agent UploadAppRuntimeFile
// 上传到目标节点的 dataRoot/apps/<appID>/.hermes/，确保多节点部署下 manager 与
// docker daemon 不必同机。
//
// 上传内容：
//   - SOUL.md：agent identity / system prompt（三层 platform + org + app 拼接）
//   - config.yaml：model provider 配置（指向 new-api），api_key 使用真实 containerAPIKey
//   - .env：凭证（OPENAI_API_KEY / OPENAI_BASE_URL + WEIXIN_DM_POLICY=open）
//
// containerAPIKey 必须是 ensureAPIKey 返回的真实 sk- token；调用方保证此函数在
// ensureAPIKey 之后执行，避免写入占位符导致 Hermes 调 new-api 返回 HTTP 401。
// runtimeFiles 为 nil 时直接报错，因为 Hermes 容器必须有这些文件才能启动。
func (h *AppInitializeHandler) writeHermesFiles(ctx context.Context, nodeID string, app sqlc.App, org sqlc.Organization, owner sqlc.User, containerAPIKey string) error {
	if h.runtimeFiles == nil {
		return fmt.Errorf("AppRuntimeFileWriter 未注入,Hermes 容器无法 bootstrap")
	}
	appID := uuidToString(app.ID)

	// 渲染 SOUL.md（平台层 + 组织层 + 应用层 prompt 三层拼接）。
	// 组织层 prompt 由 OrganizationPersona 提供，当前从 GetOrganization 不含 prompt；
	// 暂以空串占位，后续 handler 扩展接口后再补充。
	// 占位符 {org_name}/{app_name}/{owner_name} 由 VariablesFromContext 提供。
	promptResult, err := hermes.Render(hermes.PromptInput{
		PlatformPrompt: h.cfg.SystemPromptTemplate,
		OrgPrompt:      "", // 组织层 prompt 暂为空，待后续从 OrganizationPersona 注入
		AppPrompt:      textOrEmpty(app.AppPrompt),
		Variables:      hermes.VariablesFromContext(org.Name, app.Name, owner.DisplayName),
	})
	soulBody := ""
	if err != nil {
		// SOUL.md 渲染失败不阻塞容器创建:prompt 全为空时 Hermes 走默认行为,
		// 仅记错误并跳过 prompt 主体,但仍允许后续追加知识库 always-on context。
		if !errors.Is(err, hermes.ErrPromptEmpty) {
			return fmt.Errorf("渲染 SOUL.md 失败: %w", err)
		}
	} else {
		soulBody = promptResult.Prompt
	}
	// 把组织 + 应用知识库 inline 进 SOUL.md。
	// Hermes 的 skill 体系是 progressive disclosure(skills_list → skill_view),
	// agent 不主动调 skill_view 就读不到 SKILL.md 主体。SOUL.md 走 always-on 路径
	// (agent identity 一直在 system prompt),把知识库塞进 SOUL.md 才能保证
	// 用户每条消息都能命中知识库内容。
	// 应用级在前(优先级最高),组织级在后,与 spec §18 优先级语义一致。
	knowledgeInline, kerr := h.collectKnowledgeForSoul(uuidToString(app.OrgID), appID)
	if kerr != nil {
		return fmt.Errorf("拼接知识库到 SOUL.md 失败: %w", kerr)
	}
	if knowledgeInline != "" {
		if soulBody != "" {
			soulBody += "\n\n"
		}
		soulBody += knowledgeInline
	}
	if soulBody != "" {
		if err := h.runtimeFiles.UploadAppRuntimeFile(ctx, nodeID, appID, "SOUL.md", strings.NewReader(soulBody)); err != nil {
			return fmt.Errorf("上传 SOUL.md: %w", err)
		}
	}

	// 渲染 config.yaml（model provider 指向 new-api）。
	// ModelName 用 app.ModelID；NewAPIURL 来自 cfg；NewAPIToken 使用真实 containerAPIKey
	// (调用方已在 ensureAPIKey 之后再调本函数)，确保 Hermes 启动时 api_key 有效。
	// ModelID 是 string 类型（非 pgtype.Text），直接使用。
	modelName := app.ModelID
	if modelName == "" {
		modelName = h.cfg.LLM.DefaultModel
	}
	if modelName == "" {
		modelName = "default"
	}
	newAPIURL := h.cfg.NewAPIBaseURL
	if newAPIURL == "" {
		newAPIURL = "http://new-api:3000"
	}
	// containerAPIKey 由调用方 ensureAPIKey 提供(真实 sk- token)；
	// 直接写入 config.yaml api_key，避免占位符导致 Hermes 调 new-api 返回 HTTP 401。
	yamlContent, err := hermes.RenderConfigYAML(hermes.ConfigInput{
		ModelName:   modelName,
		NewAPIURL:   newAPIURL,
		NewAPIToken: containerAPIKey,
	})
	if err != nil {
		return fmt.Errorf("渲染 config.yaml 失败: %w", err)
	}
	if err := h.runtimeFiles.UploadAppRuntimeFile(ctx, nodeID, appID, "config.yaml", strings.NewReader(yamlContent)); err != nil {
		return fmt.Errorf("上传 config.yaml: %w", err)
	}

	// 渲染 .env：OPENAI_API_KEY 使用真实 token，同时含 WEIXIN_DM_POLICY=open。
	// bound 后 ChannelCheckBindingHandler 会重写 .env 追加 WEIXIN_ACCOUNT_ID 等凭证。
	envContent := hermes.RenderEnv(hermes.EnvInput{
		NewAPIURL:   newAPIURL,
		NewAPIToken: containerAPIKey,
	})
	if err := h.runtimeFiles.UploadAppRuntimeFile(ctx, nodeID, appID, ".env", strings.NewReader(envContent)); err != nil {
		return fmt.Errorf("上传 .env: %w", err)
	}

	// 把组织 / 应用知识库渲染成 .hermes/skills/kb-{org,app}-<slug>/SKILL.md,
	// Hermes 启动时按 skill 加载机制扫描该目录,使知识库内容进入 agent 上下文。
	// SkillScope 已在 DirName 前缀里区分 org / app,即便 slug 相同也不会冲突;
	// Hermes 会根据 scope 决定优先级(spec §18:应用级优先于组织级)。
	// 用 app.OrgID 取组织 ID,与 KnowledgeService 主副本路径拼接保持一致;
	// 避免依赖 GetOrganization 返回值里可能缺失的 ID 字段。
	if err := h.writeSkillsFromKnowledge(ctx, nodeID, appID, uuidToString(app.OrgID)); err != nil {
		return err
	}

	return nil
}

// collectKnowledgeForSoul 把组织 + 应用知识库主副本的所有文件读出来,
// 拼成一段适合塞进 SOUL.md 末尾的 markdown,作为 always-on 业务上下文。
//
// 顺序:应用级在组织级之前——agent 自顶向下读 system prompt,先读到的内容
// 在冲突时占优(spec §18:应用级优先于组织级,同名时应用级覆盖)。
//
// knowledge reader 未注入 / 主副本为空时返回空串(不报错)。
// 单文件超过 8KB 截断到前 8KB + 末尾标记,避免单个大文件撑爆 system prompt。
func (h *AppInitializeHandler) collectKnowledgeForSoul(orgID, appID string) (string, error) {
	if h.knowledge == nil {
		return "", nil
	}
	const perFileMax = 8 * 1024
	appPrefix := fmt.Sprintf("org/%s/app/%s/knowledge", orgID, appID)
	orgPrefix := fmt.Sprintf("org/%s/knowledge", orgID)

	// 收集器:先 app 后 org,保证 inline 顺序"应用级在前"。
	type entry struct{ scope, relPath, body string }
	var entries []entry

	collect := func(scope, prefix string) error {
		return h.knowledge.WalkFiles(prefix, func(relPath string, _ int64) error {
			reader, _, err := h.knowledge.Open(prefix + "/" + relPath)
			if err != nil {
				return err
			}
			body, readErr := io.ReadAll(reader)
			_ = reader.Close()
			if readErr != nil {
				return readErr
			}
			truncated := string(body)
			if len(truncated) > perFileMax {
				truncated = truncated[:perFileMax] + "\n\n... (后续内容已截断,完整版见 skills/kb-*-*/SKILL.md)"
			}
			entries = append(entries, entry{scope: scope, relPath: relPath, body: truncated})
			return nil
		})
	}
	if err := collect("应用级(优先生效)", appPrefix); err != nil {
		return "", err
	}
	if err := collect("组织级(默认)", orgPrefix); err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", nil
	}

	var b strings.Builder
	b.WriteString("## 业务知识库 (always-on context)\n\n")
	b.WriteString("以下是本应用所属组织 / 本应用的业务知识库内容,你必须在回答用户问题时严格按此内容回复,而非根据通用知识猜测。\n\n")
	b.WriteString("**优先级规则**:同主题下,「应用级」覆盖「组织级」——如果应用级和组织级对同一问题(如计费、话术)给出不同规则,**必须使用应用级**。\n\n")
	for _, e := range entries {
		b.WriteString(fmt.Sprintf("### %s — %s\n\n", e.scope, e.relPath))
		b.WriteString(strings.TrimSpace(e.body))
		b.WriteString("\n\n")
	}
	return b.String(), nil
}

// writeSkillsFromKnowledge 把组织 + 应用知识库主副本递归遍历并上传到
// 节点 dataRoot/apps/<appID>/.hermes/skills/。
//
// 主副本目录约定(与 service/knowledge_service.go 的 path.Join 一致):
//   - 组织: org/<orgID>/knowledge/
//   - 应用: org/<orgID>/app/<appID>/knowledge/
//
// 每个文件单独渲染一份 SKILL.md;路径冲突由 SlugifyKnowledgePath + 路径 hash
// 兜底解决。knowledge reader 未注入时直接跳过(测试装配)。
func (h *AppInitializeHandler) writeSkillsFromKnowledge(ctx context.Context, nodeID, appID, orgID string) error {
	if h.knowledge == nil {
		return nil
	}
	orgPrefix := fmt.Sprintf("org/%s/knowledge", orgID)
	if err := h.uploadKnowledgeSkills(ctx, nodeID, appID, hermes.ScopeOrg, orgPrefix); err != nil {
		return fmt.Errorf("写组织知识库 skills 失败: %w", err)
	}
	appPrefix := fmt.Sprintf("org/%s/app/%s/knowledge", orgID, appID)
	if err := h.uploadKnowledgeSkills(ctx, nodeID, appID, hermes.ScopeApp, appPrefix); err != nil {
		return fmt.Errorf("写应用知识库 skills 失败: %w", err)
	}
	return nil
}

// uploadKnowledgeSkills 遍历 prefix 目录下的每个文件,渲染成 SKILL.md 上传到
// .hermes/skills/<DirName>/SKILL.md。
// prefix 不存在视为空集(KnowledgeReader.WalkFiles 已做幂等),不报错。
func (h *AppInitializeHandler) uploadKnowledgeSkills(ctx context.Context, nodeID, appID string, scope hermes.SkillScope, prefix string) error {
	return h.knowledge.WalkFiles(prefix, func(relPath string, size int64) error {
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
		slug := hermes.SlugifyKnowledgePath(relPath)
		rendered, renderErr := hermes.RenderKnowledgeSkill(hermes.KnowledgeDoc{
			Scope: scope,
			Slug:  slug,
			Title: relPath,
			// Summary 拼成业务化引导文案,直接进 SKILL.md frontmatter description;
			// agent 会按 description 决定是否主动 /kb-* 装载本 skill。
			Summary: hermes.BuildKnowledgeSummary(scope, relPath, string(body)),
			Body:    string(body),
		})
		if renderErr != nil {
			return fmt.Errorf("渲染 SKILL.md %s 失败: %w", master, renderErr)
		}
		target := fmt.Sprintf("skills/%s/SKILL.md", rendered.DirName)
		if err := h.runtimeFiles.UploadAppRuntimeFile(ctx, nodeID, appID, target, strings.NewReader(rendered.SkillMD)); err != nil {
			return fmt.Errorf("上传 %s 失败: %w", target, err)
		}
		return nil
	})
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
	key, err := client.CreateAPIKey(ctx, newapi.CreateAPIKeyInput{
		Name:       fmt.Sprintf("app-%s", uuidToString(app.ID)),
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
