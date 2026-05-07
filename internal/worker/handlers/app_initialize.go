package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/integrations/openclaw"
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
// 容器 bind mount 前必须先在节点 agent 把 apps/<id>/{knowledge,workspace,state,logs,pi-agent}
// 5 个目录建好，否则 docker bind mount 会失败或得到 root 拥有的目录。
type AgentDirInitializer interface {
	InitAppDirs(ctx context.Context, nodeID, appID string) error
}

// AppRuntimeFileWriter 抽象在节点 agent 上写 OpenClaw 运行时配置文件的能力。
// 用于把 manager 渲染的 pi-coding-agent settings.json 上传到 apps/<id>/pi-agent/，
// 容器内 bind mount 暴露为 /root/.pi/agent/settings.json。
// nil 实现表示该装配不支持 settings.json 注入；handler 会跳过此步并继续。
type AppRuntimeFileWriter interface {
	UploadAppRuntimeFile(ctx context.Context, nodeID, appID, relPath string, content io.Reader) error
}

// ContainerStarter 抽象创建后启动容器的能力（minimal 接口）。
// 与 app_runtime_ops.go 的 ContainerLifecycle 不重叠：那个接口要求 Start/Stop/Restart/Remove
// 四个方法，初始化阶段只需要 Start，因此独立小接口便于测试 mock。
type ContainerStarter interface {
	StartContainer(ctx context.Context, nodeID, containerID string) error
}

// OpenClawHealthChecker 是 ContainerStarter 的扩展：实现该接口的 starter 在容器启动后
// 等 OpenClaw /healthz 通过才返回。AgentBackedAdapter 已实现此接口（Sprint 2 加的
// WaitForOpenClawHealthy 方法）。handler 通过类型断言探测，未实现的旧实现仍能跑通——
// 只是状态机会立即推到 binding_waiting，等 channel 流程时再失败重试。
type OpenClawHealthChecker interface {
	WaitForOpenClawHealthy(ctx context.Context, nodeID, containerID string) error
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
// 容器启动时用 Decrypt 现解作为 OPENAI_API_KEY env，全程不入日志。
//
// SystemPromptTemplate：用 {{workspace_dir}} / {{knowledge_org_dir}} / {{knowledge_app_dir}}
// 三个目录占位符，handler 在拼接 prompt 之前先按容器内目录展开（不是宿主路径）；
// 这样 OpenClaw runtime 能用容器内的相对路径直接读写文件。
//
// PlatformPrompt：第一层平台 prompt，可与 SystemPromptTemplate 同一份；
// 后续若拆分平台规约与目录约束，会在 cmd/server 装配阶段决定如何组合。
type AppInitializeConfig struct {
	RuntimeImage         string
	PlatformPrompt       string
	SystemPromptTemplate string
	Cipher               *auth.Cipher
	// ContainerNetworks 决定 manager 创建容器时连接哪些 docker network；
	// 必须包含 new-api 所在的 network，否则 OpenClaw 容器无法路由到 new-api。
	ContainerNetworks []string
	// LLM 是 OpenClaw 容器内嵌 pi-coding-agent 的模型配置；
	// BaseURL 写入容器 OPENAI_BASE_URL；DefaultProvider/DefaultModel 写入 settings.json。
	// 任一字段为空时跳过对应注入（settings.json 不会被生成），便于旧测试装配。
	LLM AppInitializeLLMConfig
}

// AppInitializeLLMConfig 是 AppInitializeConfig.LLM 的类型，与 internal/config 的
// OpenClawLLMConfig 同语义；handler 包独立定义避免反向依赖 internal/config 包。
type AppInitializeLLMConfig struct {
	BaseURL         string
	DefaultProvider string
	DefaultModel    string
}

// 容器内路径约定（runtime/openclaw/Dockerfile 与 OpenClaw runtime 共同维护）。
const (
	containerWorkspaceDir    = "/workspace"
	containerKnowledgeOrgDir = "/knowledge/org"
	containerKnowledgeAppDir = "/knowledge/app"
	containerStateDir        = "/state"
	containerLogsDir         = "/logs"
	// containerOpenClawAgentModelsPath 是 OpenClaw 上层 embedded agent 实际读取的 model catalog 文件；
	// manager 把渲染的 models.json 写到节点 apps/{id}/openclaw-config/models.json，
	// 通过 file-level bind mount（read-only）覆盖镜像内置文件，让 OpenClaw 用 manager 注入的 provider/model。
	containerOpenClawAgentModelsPath = "/root/.openclaw/agents/main/agent/models.json"
)

// AppInitializeHandler 编排应用初始化全流程。
//
// 顺序：
//  1. 加载 app/org/owner/runtime_node 上下文；
//  2. 幂等：状态 ∈ {running, binding_waiting} 直接返回成功；
//  3. 调 ImageDistributor 把 OpenClaw runtime 镜像同步到目标节点；
//  4. 调 AgentDirInitializer 在节点上准备 apps/<id>/ 4 个子目录；
//  5. 渲染 prompt（SystemPromptTemplate 展开 + Render 三层拼接）；
//  6. api_key 不 active 时调 new-api 创建并 cipher.Encrypt 写库；
//  7. container_id 为空时调 ContainerCreator.CreateContainer，把 ID/Name 写库；
//  8. 调 ContainerLifecycle.StartContainer 启动容器；
//  9. 推 status=binding_waiting，由 channel 流程接管后续。
//
// 任意一步失败立即冒泡，由 worker 重试或入 failed；状态机字段只在显式步骤里单独写。
//
// 各依赖均可为 nil 实现降级：
//   - containers / starter / dirs nil 时该步骤被跳过，方便在最小装配下走通
//     api_key + 状态推进路径；生产装配必须全部非 nil
type AppInitializeHandler struct {
	store        AppInitializeStore
	images       ImageDistributor
	dirs         AgentDirInitializer
	runtimeFiles AppRuntimeFileWriter
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
		cfg.RuntimeImage = "openclaw-runtime:dev"
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

// SetRuntimeFileWriter 注入 settings.json 上传能力。
// agent 装配未就绪或测试场景可不调用，handler 会跳过 settings.json 写入。
func (h *AppInitializeHandler) SetRuntimeFileWriter(w AppRuntimeFileWriter) {
	h.runtimeFiles = w
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
			return fmt.Errorf("分发 OpenClaw 镜像失败: %w", err)
		}
	}

	// 容器创建前先让节点 agent 准备好 apps/<id>/{knowledge,workspace,state,logs,pi-agent} 5 个目录，
	// 否则 docker bind mount 失败或得到 root 拥有的空目录。InitAppDirs 幂等。
	if h.dirs != nil && payload.RuntimeNodeID != "" {
		if err := h.dirs.InitAppDirs(ctx, payload.RuntimeNodeID, payload.AppID); err != nil {
			return fmt.Errorf("初始化节点应用目录失败: %w", err)
		}
	}

	// 写 OpenClaw 上层 embedded agent 的 models.json：把 cfg.LLM 渲染成单 provider/single-model
	// catalog，通过 file-level bind mount 覆盖镜像自带 /root/.openclaw/agents/main/agent/models.json，
	// 让上层 agent 用 manager 指定的 provider+model（如 openai → new-api → ollama qwen2.5:0.5b）。
	// cfg.LLM 任一关键字段为空时跳过上传，OpenClaw 沿用镜像默认（codex/gpt-5.5）。
	if h.runtimeFiles != nil && payload.RuntimeNodeID != "" {
		if models := renderOpenClawModels(h.cfg.LLM); models != nil {
			if err := h.runtimeFiles.UploadAppRuntimeFile(ctx, payload.RuntimeNodeID, payload.AppID, "models.json", bytes.NewReader(models)); err != nil {
				return fmt.Errorf("写 OpenClaw models.json 失败: %w", err)
			}
		}
	}

	systemPrompt, err := h.renderSystemPrompt()
	if err != nil {
		return err
	}
	composed, err := openclaw.Render(openclaw.PromptInput{
		PlatformPrompt: firstNonEmpty(systemPrompt, h.cfg.PlatformPrompt),
		OrgPrompt:      "",
		AppPrompt:      textOrEmpty(app.AppPrompt),
		Variables:      openclaw.VariablesFromContext(org.Name, app.Name, owner.DisplayName),
	})
	if err != nil {
		return fmt.Errorf("渲染 prompt 失败: %w", err)
	}

	containerAPIKey, err := h.ensureAPIKey(ctx, &app)
	if err != nil {
		return err
	}
	_ = org // org 已在上文用于 prompt；ensureAPIKey 现在通过 factory 自行获取组织凭据。

	if app.ContainerID.String == "" && h.containers != nil {
		node, err := h.store.GetRuntimeNode(ctx, app.RuntimeNodeID)
		if err != nil {
			return fmt.Errorf("查询 runtime node 失败: %w", err)
		}
		spec := buildContainerSpec(buildSpecArgs{
			AppID:             payload.AppID,
			OrgID:             uuidToString(app.OrgID),
			RuntimeImage:      h.cfg.RuntimeImage,
			NodeDataRoot:      node.NodeDataRoot.String,
			SystemPrompt:      composed.Prompt,
			APIKey:            containerAPIKey,
			NewAPIBaseURL:     "", // 旧字段，留空以保持兼容；OpenAI base URL 通过 LLMBaseURL 注入
			LLMBaseURL:        h.cfg.LLM.BaseURL,
			ContainerNetworks: h.cfg.ContainerNetworks,
		})
		info, err := h.containers.CreateContainer(ctx, payload.RuntimeNodeID, spec)
		if err != nil {
			return fmt.Errorf("创建容器失败: %w", err)
		}
		if _, err := h.store.SetAppContainer(ctx, sqlc.SetAppContainerParams{
			ID:            app.ID,
			ContainerID:   pgtype.Text{String: info.ID, Valid: info.ID != ""},
			ContainerName: pgtype.Text{String: info.Name, Valid: info.Name != ""},
		}); err != nil {
			return fmt.Errorf("写入 container_id 失败: %w", err)
		}
		// 立刻启动容器；OpenClaw gateway 启动需 ~10s 加载 plugin。
		if h.starter != nil && info.ID != "" {
			if err := h.starter.StartContainer(ctx, payload.RuntimeNodeID, info.ID); err != nil {
				return fmt.Errorf("启动容器失败: %w", err)
			}
			// Sprint 2：starter 实现 OpenClawHealthChecker 时等 /healthz 通过再推 binding_waiting，
			// 避免应用过早进入待绑定状态导致后续 channel_start_login 直接撞到 plugin 未就绪。
			if checker, ok := h.starter.(OpenClawHealthChecker); ok {
				if err := checker.WaitForOpenClawHealthy(ctx, payload.RuntimeNodeID, info.ID); err != nil {
					return fmt.Errorf("等待 OpenClaw 健康失败: %w", err)
				}
			}
		}
	}

	if app.Status != domain.AppStatusBindingWaiting {
		if _, err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{
			ID:     app.ID,
			Status: domain.AppStatusBindingWaiting,
		}); err != nil {
			return fmt.Errorf("更新应用状态失败: %w", err)
		}
	}
	return nil
}

// ensureAPIKey 走「以组织业务 user 身份创 token + 拉完整 sk-」流程，加密落库后返回明文 sk-。
//
// 已经 active 的应用直接读 ciphertext 解密返回，避免重复创建。
// 解密失败 / 拉 key 失败都直接报错；不再有"全局 fallback sk-"的兜底路径
// （以前 cfg.LLM.OpenAICompatAPIKey 那条路已经下线，参见本次改造的设计文档）。
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
	// 计费与额度归 new-api 的 user 级管理。如果 unlimited=false 且 Quota=0，
	// new-api 会在 chat/completions 时报"Invalid token"（实际是 quota exhausted），让 OpenClaw
	// 上层把所有用户消息都当成 LLM 错误回复。
	key, err := client.CreateAPIKey(ctx, newapi.CreateAPIKeyInput{
		Name:       fmt.Sprintf("app-%s", uuidToString(app.ID)),
		Models:     []string{},
		UnlimitedQ: true,
	})
	if err != nil {
		return "", fmt.Errorf("调用 new-api 创建 api_key 失败: %w", err)
	}
	if key.ID == 0 {
		return "", fmt.Errorf("new-api CreateAPIKey 返回 token id=0")
	}

	fullKey, err := client.GetTokenFullKey(ctx, key.ID)
	if err != nil {
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

// renderSystemPrompt 把模板中的 {{workspace_dir}} / {{knowledge_org_dir}} / {{knowledge_app_dir}}
// 替换成容器内固定路径；模板缺失时返回空串，让上层走 PlatformPrompt 兜底。
func (h *AppInitializeHandler) renderSystemPrompt() (string, error) {
	template := h.cfg.SystemPromptTemplate
	if strings.TrimSpace(template) == "" {
		return "", nil
	}
	expanded := template
	for _, sub := range []struct {
		placeholder string
		value       string
	}{
		{"{{workspace_dir}}", containerWorkspaceDir},
		{"{{knowledge_org_dir}}", containerKnowledgeOrgDir},
		{"{{knowledge_app_dir}}", containerKnowledgeAppDir},
	} {
		expanded = strings.ReplaceAll(expanded, sub.placeholder, sub.value)
	}
	return expanded, nil
}

// buildSpecArgs 集中描述 buildContainerSpec 需要的输入，避免一长串位置参数。
type buildSpecArgs struct {
	AppID         string
	OrgID         string
	RuntimeImage  string
	NodeDataRoot  string
	SystemPrompt  string
	APIKey        string
	NewAPIBaseURL string
	// LLMBaseURL 是 OpenClaw 容器内 pi-coding-agent 调模型的 OpenAI 兼容网关地址（含 /v1）；
	// 来自 cfg.OpenClaw.LLM.BaseURL，覆盖 NewAPIBaseURL（后者无 /v1 后缀，OpenAI SDK 不能直接用）。
	// 留空时 fallback 到 NewAPIBaseURL（兼容旧测试 / 部署）。
	LLMBaseURL string
	// ContainerNetworks 是 manager 创建容器时要连接的 docker network 列表；
	// 必须包含 new-api 所在 network，否则 OpenClaw 容器无法解析 new-api hostname。
	ContainerNetworks []string
}

// buildContainerSpec 按 spec §A2 的目录与 env 约定构造 ContainerSpec。
//
// 容器名固定为 ocm-{app_id} 便于 docker ps 一眼定位；挂载使用 host bind 而非 named volume，
// 与项目"禁止 named volume"约束一致；env 中保留明文 OPENCLAW_API_KEY，OpenClaw runtime
// 直接读它调 new-api，manager 端不再保留任何明文副本。
func buildContainerSpec(args buildSpecArgs) runtimepkg.ContainerSpec {
	dataRoot := strings.TrimRight(args.NodeDataRoot, "/")
	if dataRoot == "" {
		dataRoot = "/var/lib/oc-agent"
	}
	appDir := path.Join(dataRoot, "apps", args.AppID)
	orgKnowledge := path.Join(dataRoot, "orgs", args.OrgID, "knowledge")
	openaiBaseURL := args.LLMBaseURL
	if openaiBaseURL == "" {
		openaiBaseURL = args.NewAPIBaseURL
	}
	return runtimepkg.ContainerSpec{
		Name:     "ocm-" + args.AppID,
		Image:    args.RuntimeImage,
		Networks: args.ContainerNetworks,
		Env: map[string]string{
			// Sprint 0 实测：上游 OpenClaw 内置 openai@^6.x SDK，识别标准
			// OPENAI_API_KEY / OPENAI_BASE_URL 环境变量；不是 OPENCLAW_*
			"OPENAI_API_KEY":             args.APIKey,
			"OPENAI_BASE_URL":            openaiBaseURL,
			"OPENCLAW_SYSTEM_PROMPT":     args.SystemPrompt,
			"OPENCLAW_WORKSPACE_DIR":     containerWorkspaceDir,
			"OPENCLAW_KNOWLEDGE_ORG_DIR": containerKnowledgeOrgDir,
			"OPENCLAW_KNOWLEDGE_APP_DIR": containerKnowledgeAppDir,
			// Sprint 0 实测：上游 docs/install/docker.md 推荐容器场景禁 mDNS 广播
			"OPENCLAW_DISABLE_BONJOUR": "1",
		},
		Volumes: []runtimepkg.VolumeMount{
			{HostPath: path.Join(appDir, "workspace"), ContainerPath: containerWorkspaceDir},
			{HostPath: orgKnowledge, ContainerPath: containerKnowledgeOrgDir, ReadOnly: true},
			{HostPath: path.Join(appDir, "knowledge"), ContainerPath: containerKnowledgeAppDir, ReadOnly: true},
			{HostPath: path.Join(appDir, "state"), ContainerPath: containerStateDir},
			{HostPath: path.Join(appDir, "logs"), ContainerPath: containerLogsDir},
			// models.json 由 manager 在 InitAppDirs 之后通过 agent UploadAppRuntimeFile 写到
			// apps/<id>/openclaw-config/models.json，再以 file-level bind mount 覆盖容器内
			// OpenClaw 镜像自带的 models.json，让上层 embedded agent 走 manager 注入的
			// provider/model（如 openai → new-api → ollama qwen2.5:0.5b）。
			//
			// 不能设 ReadOnly：OpenClaw 启动时会 rename tmp → models.json 做 atomic update
			// （merge mode 合并 build-in），RO 文件 mount 会让 rename 失败 → catalog 加载
			// 报 EBUSY → fallback 到默认 codex/gpt-5.5。允许写也无碍：OpenClaw 写回的内容
			// 仅在容器生命周期内有效，下次 init 仍由 manager 重写。
			//
			// 已知 warning：file-level bind mount 不允许 rename target == mount point，
			// OpenClaw startup 的"model warmup"会报 `EBUSY: rename .json.tmp -> .json`
			// 但功能不受影响——通过 openclaw.json 的 models.mode=replace + providers 注入
			// 后，embedded agent 直接用 openclaw.json 里的 catalog，warmup 失败属于 cosmetic
			// log 噪音。彻底消除需要改 OpenClaw 上游不在 RW mount 文件上做 rename，
			// 或 manager 改为在容器启动后通过 docker exec 写文件而不用 mount。
			{HostPath: path.Join(appDir, "openclaw-config", "models.json"), ContainerPath: containerOpenClawAgentModelsPath},
		},
	}
}

// renderOpenClawModels 把 LLM 配置渲染成 OpenClaw 上层 embedded agent 的 models.json
// 内容；通过 file-level bind mount 覆盖镜像自带文件，让 OpenClaw 走 manager 注入的
// provider/model。
//
// 任一关键字段（BaseURL / DefaultProvider / DefaultModel）为空返回 nil，调用方应跳过
// 上传，OpenClaw 沿用镜像内置 models.json（默认 codex/gpt-5.5）。
//
// 输出结构对齐 OpenClaw 镜像内置 models.json 的真实 schema：
//
//	{
//	  "providers": {
//	    "<provider>": {
//	      "baseUrl": "...",
//	      "apiKey": "${OPENAI_API_KEY}",   // 由容器 env OPENAI_API_KEY 提供
//	      "auth": "token",
//	      "api": "openai",
//	      "models": [{"id": "...", "name": "...", "api": "openai", ...}]
//	    }
//	  }
//	}
//
// apiKey 字段写明文 "${OPENAI_API_KEY}"——OpenClaw 内部会做 env 展开，让 manager 不需
// 要在 models.json 里持久化真 token；真 token 通过 OPENAI_API_KEY env 注入容器。
func renderOpenClawModels(cfg AppInitializeLLMConfig) []byte {
	provider := strings.TrimSpace(cfg.DefaultProvider)
	model := strings.TrimSpace(cfg.DefaultModel)
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if provider == "" || model == "" || baseURL == "" {
		return nil
	}
	doc := map[string]any{
		"providers": map[string]any{
			provider: map[string]any{
				"baseUrl": baseURL,
				"apiKey":  "${OPENAI_API_KEY}",
				"auth":    "token",
				"api":     "openai",
				"models": []any{
					map[string]any{
						"id":            model,
						"name":          model,
						"api":           "openai",
						"input":         []string{"text"},
						"cost":          map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
						"contextWindow": 32768,
						"maxTokens":     4096,
					},
				},
			},
		},
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil
	}
	return raw
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

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
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
