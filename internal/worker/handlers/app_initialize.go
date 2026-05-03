package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
// 容器 bind mount 前必须先在节点 agent 把 apps/<id>/{knowledge,workspace,state,logs}
// 4 个目录建好，否则 docker bind mount 会失败或得到 root 拥有的目录。
type AgentDirInitializer interface {
	InitAppDirs(ctx context.Context, nodeID, appID string) error
}

// ContainerStarter 抽象创建后启动容器的能力（minimal 接口）。
// 与 app_runtime_ops.go 的 ContainerLifecycle 不重叠：那个接口要求 Start/Stop/Restart/Remove
// 四个方法，初始化阶段只需要 Start，因此独立小接口便于测试 mock。
type ContainerStarter interface {
	StartContainer(ctx context.Context, nodeID, containerID string) error
}

// NewAPIClient 是 worker 与 new-api 交互的最小集合。
type NewAPIClient interface {
	CreateAPIKey(ctx context.Context, input newapi.CreateAPIKeyInput) (newapi.APIKey, error)
}

// AppInitializeConfig 提供 handler 运行所需的外部配置。
//
// Cipher：把 new-api 返回的 api_key 明文加密后写入 newapi_key_ciphertext，
// 容器启动时用 Decrypt 现解作为 OPENCLAW_API_KEY env，全程不入日志。
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
}

// 容器内路径约定（runtime/openclaw/Dockerfile 与 OpenClaw runtime 共同维护）。
const (
	containerWorkspaceDir    = "/workspace"
	containerKnowledgeOrgDir = "/knowledge/org"
	containerKnowledgeAppDir = "/knowledge/app"
	containerStateDir        = "/state"
	containerLogsDir         = "/logs"
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
	store      AppInitializeStore
	images     ImageDistributor
	dirs       AgentDirInitializer
	containers ContainerCreator
	starter    ContainerStarter
	newapi     NewAPIClient
	cfg        AppInitializeConfig
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
	client NewAPIClient,
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
		newapi:     client,
		cfg:        cfg,
	}
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

	// 容器创建前先让节点 agent 准备好 apps/<id>/{knowledge,workspace,state,logs} 4 个目录，
	// 否则 docker bind mount 失败或得到 root 拥有的空目录。InitAppDirs 幂等。
	if h.dirs != nil && payload.RuntimeNodeID != "" {
		if err := h.dirs.InitAppDirs(ctx, payload.RuntimeNodeID, payload.AppID); err != nil {
			return fmt.Errorf("初始化节点应用目录失败: %w", err)
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

	apiKeyPlaintext, err := h.ensureAPIKey(ctx, &app, org)
	if err != nil {
		return err
	}

	if app.ContainerID.String == "" && h.containers != nil {
		node, err := h.store.GetRuntimeNode(ctx, app.RuntimeNodeID)
		if err != nil {
			return fmt.Errorf("查询 runtime node 失败: %w", err)
		}
		spec := buildContainerSpec(buildSpecArgs{
			AppID:         payload.AppID,
			OrgID:         uuidToString(app.OrgID),
			RuntimeImage:  h.cfg.RuntimeImage,
			NodeDataRoot:  node.NodeDataRoot.String,
			SystemPrompt:  composed.Prompt,
			APIKey:        apiKeyPlaintext,
			NewAPIBaseURL: "", // 由后续 cmd/server 注入；本测试不依赖
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
		// 立刻启动容器；OpenClaw gateway 启动需 ~10s 加载 plugin，状态机后续在
		// channel 流程里通过 health 探针确认 ready，这里只发出 start 信号。
		if h.starter != nil && info.ID != "" {
			if err := h.starter.StartContainer(ctx, payload.RuntimeNodeID, info.ID); err != nil {
				return fmt.Errorf("启动容器失败: %w", err)
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

// ensureAPIKey 在必要时通过 new-api 创建 api_key 并加密落库。
// 已经 active 的应用直接读现有 ciphertext 解密返回；缺 cipher 时回退为 ciphertext 原值。
func (h *AppInitializeHandler) ensureAPIKey(ctx context.Context, app *sqlc.App, org sqlc.Organization) (string, error) {
	if app.ApiKeyStatus == domain.APIKeyStatusActive {
		// 已经有 api_key：尝试解密供容器使用。
		return decryptIfNeeded(app.NewapiKeyCiphertext.String, h.cfg.Cipher)
	}
	if h.newapi == nil {
		return "", fmt.Errorf("new-api client 未配置，无法创建 api_key")
	}
	key, err := h.newapi.CreateAPIKey(ctx, newapi.CreateAPIKeyInput{
		Name:   fmt.Sprintf("%s-%s", org.Name, app.Name),
		Models: []string{},
		Quota:  0,
	})
	if err != nil {
		return "", fmt.Errorf("调用 new-api 创建 api_key 失败: %w", err)
	}
	ciphertext, err := encryptIfNeeded(key.Key, h.cfg.Cipher)
	if err != nil {
		return "", fmt.Errorf("加密 api_key 失败: %w", err)
	}
	updated, err := h.store.SetAppNewAPIKey(ctx, sqlc.SetAppNewAPIKeyParams{
		ID:                  app.ID,
		NewapiKeyID:         pgtype.Text{String: fmt.Sprintf("%d", key.ID), Valid: key.ID != 0},
		NewapiKeyCiphertext: pgtype.Text{String: ciphertext, Valid: ciphertext != ""},
		ApiKeyStatus:        domain.APIKeyStatusActive,
	})
	if err != nil {
		return "", fmt.Errorf("写入 api_key 失败: %w", err)
	}
	*app = updated
	return key.Key, nil
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
	return runtimepkg.ContainerSpec{
		Name:  "ocm-" + args.AppID,
		Image: args.RuntimeImage,
		Env: map[string]string{
			// Sprint 0 实测：上游 OpenClaw 内置 openai@^6.x SDK，识别标准
			// OPENAI_API_KEY / OPENAI_BASE_URL 环境变量；不是 OPENCLAW_*
			"OPENAI_API_KEY":             args.APIKey,
			"OPENAI_BASE_URL":            args.NewAPIBaseURL,
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
		},
	}
}

// encryptIfNeeded 把明文 api_key 加密；cipher 为 nil 时直接返回明文，
// 让最早期没装配 cipher 的部署仍能跑通；生产部署强制 cipher 非 nil。
func encryptIfNeeded(plaintext string, cipher *auth.Cipher) (string, error) {
	if cipher == nil {
		return plaintext, nil
	}
	return cipher.Encrypt([]byte(plaintext))
}

// decryptIfNeeded 把 ciphertext 解密为明文；cipher 为 nil 时把 ciphertext 视作明文。
func decryptIfNeeded(ciphertext string, cipher *auth.Cipher) (string, error) {
	if ciphertext == "" || cipher == nil {
		return ciphertext, nil
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
