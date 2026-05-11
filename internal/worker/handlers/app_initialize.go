package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/audit"
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

// ContainerExecer 抽象「在容器内执行命令」的能力，handler 用它在 OpenClaw 容器启动后
// 注入 agents.defaults.model 配置。OpenClaw 上游 default model 是 `openai/gpt-5.5`
// （v1.0.1 GA 已知降级），manager 拿到 LLM 配置后必须显式 patch `agents.defaults.model`
// 字段，否则 weixin 触发的 embedded agent 会调 api.openai.com 被 OpenClaw 沙箱拦截。
//
// 实现要求：ContainerExec 同步等命令结束并返回 exit code（AgentBackedAdapter 已是
// 这个语义）。
//
// 关键设计：**不**触发 docker restart。OpenClaw gateway 自身有 fs watcher 监听
// openclaw.json 变化（日志可见 `[reload] config change detected; evaluating reload`），
// 写完配置后自动 hot reload。如果 manager 紧跟 docker restart，会撞到 OpenClaw
// 重新加载 plugin 失败的已知问题（启动后日志 `http server listening (0 plugins, 2.0s)`，
// weixin plugin 不加载）。让 OpenClaw 自己 reload 是最稳妥路径。
//
// 接口存在的目的：保持 ContainerStarter 最小（只一个方法）便于既有测试 mock，
// 通过类型断言把扩展能力注入特定路径，未实现该接口的 starter 跳过 model 注入但仍能
// 完成容器启动 + healthcheck 全链路（OpenClaw 沿用默认 gpt-5.5，依赖 ops 在 new-api
// 渠道做 model alias 兜底）。
type ContainerExecer interface {
	ContainerExec(ctx context.Context, nodeID, containerID string, cmd []string) (runtimepkg.ExecResult, error)
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
	// AuditHelper 在 new-api 调用失败时写 audit_logs.target_type=newapi_call。
	// nil 时跳过审计，不影响主流程；生产装配应注入。
	AuditHelper *audit.NewAPIAuditHelper
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
	// containerWeixinPluginDataDir 是 OpenClaw weixin 渠道插件持久化 token / accounts.json 的目录。
	// 上游 plugin 默认写在 /root/.openclaw/openclaw-weixin/，属于容器 ephemeral 路径——
	// docker restart 后丢失会导致 weixin sidecar 启动时不 spawn provider（gateway 日志
	// `starting channels and sidecars` 后无 weixin starting 行），消息收不到。
	// manager 通过 bind mount 把 host 上的 apps/{id}/weixin/ 挂进来，token 跟着 app 生命周期持久化。
	containerWeixinPluginDataDir = "/root/.openclaw/openclaw-weixin"
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

	// 不再写 apps/{id}/openclaw-config/models.json + file-level bind mount。
	//
	// 之前实现把 manager 渲染的 catalog 通过 RW 文件级 bind mount 覆盖容器内
	// /root/.openclaw/agents/main/agent/models.json，但 OpenClaw embedded agent 在响应
	// 用户消息时会 atomic rename .tmp -> models.json 做 catalog 自更新，撞 mount point
	// 触发 EBUSY → "Embedded agent failed before reply"，weixin 回 "Something went wrong"。
	// v1.0.1 GA 验证报告把这个误判为 cosmetic warning，实际上每次新建容器都重现。
	//
	// 修复改为：容器启动后由 worker docker exec `openclaw config patch --stdin` 写
	// models.providers + models.mode=replace 到 openclaw.json（OpenClaw 自身 fs watcher
	// 监听的配置文件，无 mount 冲突），由 OpenClaw hot reload 生效。详见 §「容器启动后
	// 的 OpenClaw 配置注入」。

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
			// v1.0.2：OpenClaw 默认 agents.defaults.model 是 gpt-5.5；manager 拿到 LLM 配置后
			// 必须显式 patch 进容器内的 openclaw.json，否则 embedded agent 会调 api.openai.com。
			// `openclaw config set` 写文件后 OpenClaw gateway 自身 fs watcher 自动 hot reload，
			// 不需要 docker restart（restart 会让 weixin plugin 加载失败）。
			if execer, ok := h.starter.(ContainerExecer); ok && h.cfg.LLM.DefaultModel != "" {
				if err := configureOpenClawDefaultModel(ctx, execer, payload.RuntimeNodeID, info.ID, h.cfg.LLM); err != nil {
					// 模型注入失败不阻塞主流程：容器仍会用 gpt-5.5 默认值；ops 可在 new-api 端
					// 用 model_mapping 兜底（gpt-5.5 -> qwen2.5:0.5b）。但记日志便于排查。
					slog.WarnContext(ctx, "app_initialize: 配置 openclaw 默认 model 失败", "app_id", uuidToString(app.ID), "error", err)
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
			// weixin plugin token 持久化目录：闭合 v1.0.1 GA 验证报告里"重建容器丢 weixin
			// token state，需要 docker cp 备份/恢复"的 deployment workaround。挂上以后
			// docker restart / docker rm 重建都不会丢扫码 session，consumer 重新启动后
			// plugin sidecar 看到 accounts/<account>.json 直接 resume，不需要重新扫码。
			{HostPath: path.Join(appDir, "weixin"), ContainerPath: containerWeixinPluginDataDir},
			// 注：models.json file-level bind mount 已删除。之前实现把 manager 渲染的
			// catalog 通过 RW 文件级 bind mount 覆盖容器内
			// /root/.openclaw/agents/main/agent/models.json，但 OpenClaw embedded agent
			// 在响应用户消息时会 atomic rename .tmp -> models.json，撞 mount point 触发
			// EBUSY，导致 weixin 回 "Something went wrong"。改为容器启动后由 worker 用
			// `openclaw config patch` 把 models.providers 写到 openclaw.json，规避 mount 冲突。
		},
	}
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

// configureOpenClawDefaultModel 在容器内执行 `openclaw config set agents.defaults.model <model>`，
// 让 OpenClaw gateway 自身的 fs watcher 检测到 openclaw.json 变化后 hot reload。
//
// 实现要点：
//   - 命令通过 docker exec 同步执行，等 exit code == 0 才认为成功；非 0 视为失败但不
//     panic；调用方 swallow 错误并 log，主流程继续（容器仍可启动，仅默认 model 是 gpt-5.5）。
//   - openclaw CLI 输出含 ANSI 框线和 plugin manifest warning 等 noise，不解析 stdout，
//     仅看 exit code 与"Updated agents.defaults.model"标记串。
//   - 不触发 docker restart：openclaw 内部约 17s 内自动检测 fs 变化并 reload，
//     提示 "Restart the gateway to apply" 仅是 CLI 兜底建议。manager 这层 docker
//     restart 反而会让 plugin 重新加载失败（v1.0.2 实测，weixin plugin 0 个）。
func configureOpenClawDefaultModel(ctx context.Context, execer ContainerExecer, nodeID, containerID string, llm AppInitializeLLMConfig) error {
	provider := strings.TrimSpace(llm.DefaultProvider)
	model := strings.TrimSpace(llm.DefaultModel)
	baseURL := strings.TrimSpace(llm.BaseURL)
	if provider == "" || model == "" || baseURL == "" {
		// 配置不齐时跳过，OpenClaw 沿用镜像默认（gpt-5.5）。
		return nil
	}
	patch := map[string]any{
		"agents": map[string]any{
			"defaults": map[string]any{"model": model},
		},
		"models": map[string]any{
			// replace：embedded agent 直接用注入的 catalog，不 merge 镜像内置（默认 codex/
			// gpt-5.5），避免 catalog 自更新时 rename 触发的 EBUSY。
			"mode": "replace",
			"providers": map[string]any{
				provider: map[string]any{
					"baseUrl": baseURL,
					"apiKey":  "${OPENAI_API_KEY}",
					// schema 要求每个 model entry 必填 id + name（minLength=1）。
					"models": []any{map[string]any{"id": model, "name": model}},
				},
			},
		},
	}
	body, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("序列化 openclaw patch 失败: %w", err)
	}
	// `openclaw config patch --stdin` 从 stdin 读 JSON5；ContainerExec 没有 stdin 通道，
	// 用 sh -c 把 body echo 进去。body 内 ${OPENAI_API_KEY} 字面值在单引号里不会被
	// shell 展开，直接写到 openclaw.json，OpenClaw 读取时再展开 ENV。
	cmd := []string{"sh", "-c", fmt.Sprintf("echo %s | openclaw config patch --stdin", shellQuote(string(body)))}
	res, err := execer.ContainerExec(ctx, nodeID, containerID, cmd)
	if err != nil {
		return fmt.Errorf("docker exec openclaw config patch 失败: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("openclaw config patch 返回 exit=%d stdout=%q", res.ExitCode, res.Stdout)
	}
	// CLI 在成功路径输出 "Patched" 或 "Updated"；缺失只记警告不 fail。
	if !strings.Contains(res.Stdout, "Patched") && !strings.Contains(res.Stdout, "Updated") {
		slog.WarnContext(ctx, "app_initialize: openclaw config patch 未输出预期 ack", "stdout", res.Stdout)
	}
	return nil
}

// shellQuote 用单引号包裹字符串供 sh -c 使用，并按 shell 规则转义内部单引号
// （先闭合单引号，再写入反斜杠转义的单引号，最后重开单引号）。
// 仅用于 manager 渲染的可控 JSON，不接受用户输入。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
