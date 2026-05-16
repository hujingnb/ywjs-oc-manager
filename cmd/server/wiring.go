// 装配 manager 进程所需的所有跨包依赖。
// 这里只放"接口适配 + 多节点路由"等胶水逻辑；具体业务规则由 service 层负责。
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	dockercli "github.com/docker/docker/client"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/agent"
	"oc-manager/internal/integrations/hermes"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/integrations/runtime"
	"oc-manager/internal/service"
	"oc-manager/internal/store"
	"oc-manager/internal/store/sqlc"
	workerhandlers "oc-manager/internal/worker/handlers"
)

// nodeQueries 是 nodeClientResolver 需要的最小查询子集。
// 抽出接口便于测试用内存桩。
type nodeQueries interface {
	GetRuntimeNode(ctx context.Context, id pgtype.UUID) (sqlc.RuntimeNode, error)
}

// nodeClientResolver 把 nodeID 翻译为面向单节点的多种 client。
//
// 同时实现了：
//   - runtime.AgentResolver（FileClient）
//   - runtime.DockerClientResolver / channel.DockerClientResolver（DockerClient）
//
// 之所以聚合到一个类型：每次都要先按 nodeID 查 runtime_node 行 + 取 token resolver，
// 散到多个类型只会重复样板代码。
type nodeClientResolver struct {
	queries nodeQueries
	tokens  *agent.TokenResolver
}

func newNodeClientResolver(queries nodeQueries, tokens *agent.TokenResolver) *nodeClientResolver {
	return &nodeClientResolver{
		queries: queries,
		tokens:  tokens,
	}
}

// FileClient 取 agent 文件 API client（plaintext，B 阶段后再加 TLS）。
func (n *nodeClientResolver) FileClient(ctx context.Context, nodeID string) (*agent.AgentFileClient, error) {
	node, token, err := n.lookupNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if !node.AgentFileEndpoint.Valid || strings.TrimSpace(node.AgentFileEndpoint.String) == "" {
		return nil, fmt.Errorf("节点 %s 未注册 agent_file_endpoint", nodeID)
	}
	httpClient, err := n.agentHTTPClient(node, 30*time.Second)
	if err != nil {
		return nil, err
	}
	client := agent.NewFileClient(node.AgentFileEndpoint.String, token)
	client.SetHTTPClient(httpClient)
	return client, nil
}

// DockerClient 取面向单节点的 docker SDK client（HTTPS + Bearer）。
func (n *nodeClientResolver) DockerClient(ctx context.Context, nodeID string) (*dockercli.Client, error) {
	node, token, err := n.lookupNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if !node.AgentDockerEndpoint.Valid || strings.TrimSpace(node.AgentDockerEndpoint.String) == "" {
		return nil, fmt.Errorf("节点 %s 未注册 agent_docker_endpoint", nodeID)
	}
	if !node.AgentTlsCaCert.Valid || strings.TrimSpace(node.AgentTlsCaCert.String) == "" {
		return nil, fmt.Errorf("节点 %s 缺 agent_tls_ca_cert", nodeID)
	}
	return agent.NewDockerClientForNode(node.AgentDockerEndpoint.String, token, node.AgentTlsCaCert.String)
}

// streamingDockerResolver 适配 channel.DockerClientResolver,返回无 timeout 的 docker client,
// 专门给微信扫码 ExecAttach 这类长连接场景用。
//
// 背景:nodeClientResolver.DockerClient 给 http.Client 设 Timeout=30s 防 worker hang,
// 但 ExecAttach hijack 后还是受同一个 client.Timeout 影响,30s 后底层连接被强制 close,
// 导致 docker stream EOF + JSON 解析失败 + 容器内 oc-weixin-login.py 进程 orphan hang。
// 此 resolver 用 agent.NewStreamingDockerClientForNode 构造没有 client.Timeout 的 client,
// 让 attach 流可以持续到 oc-weixin-login.py 主动退出(用户扫码完成或超时)。
type streamingDockerResolver struct {
	inner *nodeClientResolver
}

func newStreamingDockerResolver(inner *nodeClientResolver) *streamingDockerResolver {
	return &streamingDockerResolver{inner: inner}
}

// DockerClient 实现 channel.DockerClientResolver,返回禁用 timeout 的长连接 docker client。
func (s *streamingDockerResolver) DockerClient(ctx context.Context, nodeID string) (*dockercli.Client, error) {
	node, token, err := s.inner.lookupNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if !node.AgentDockerEndpoint.Valid || strings.TrimSpace(node.AgentDockerEndpoint.String) == "" {
		return nil, fmt.Errorf("节点 %s 未注册 agent_docker_endpoint", nodeID)
	}
	if !node.AgentTlsCaCert.Valid || strings.TrimSpace(node.AgentTlsCaCert.String) == "" {
		return nil, fmt.Errorf("节点 %s 缺 agent_tls_ca_cert", nodeID)
	}
	return agent.NewStreamingDockerClientForNode(node.AgentDockerEndpoint.String, token, node.AgentTlsCaCert.String)
}

// hermesConfigQueries 是 hermesConfigRefresher 需要的最小查询子集。
// 抽出接口便于测试。
// 包含 GetOrganization / GetUser 以支持 restart 时重渲 SOUL.md
// (用 org.Name / owner.DisplayName 替换占位符)。
type hermesConfigQueries interface {
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	GetOrganization(ctx context.Context, id pgtype.UUID) (sqlc.Organization, error)
	GetUser(ctx context.Context, id pgtype.UUID) (sqlc.User, error)
}

// hermesConfigUploader 抽象向目标节点上传 Hermes 配置文件的能力。
// runtime.AgentBackedAdapter 已实现 UploadAppRuntimeFile,满足此接口。
// 同时含 InitAppDirs:restart 路径调用一次保证 .hermes/workspace 等关键
// 子目录存在(早期版本 init 时没建 workspace,升级后的存量 app 也能补建)。
type hermesConfigUploader interface {
	UploadAppRuntimeFile(ctx context.Context, nodeID, appID, relPath string, content io.Reader) error
	InitAppDirs(ctx context.Context, nodeID, appID string) error
}

// hermesConfigRefresher 实现 workerhandlers.HermesConfigRefresher,
// 在 restart job 触发时根据 DB 当前 app.model_id + 解密的 OPENAI_API_KEY
// 重新渲染 Hermes 的 config.yaml 并通过 runtime-agent 上传到容器。
//
// 同时遍历组织/应用知识库主副本,把每个文件渲染成 .hermes/skills/kb-*-<slug>/SKILL.md
// 上传到容器——这样 restart 后 Hermes 容器内 skills 与 manager 主副本保持一致,
// 不必依赖单独的 knowledge_sync_node 全量重推。
//
// 不刷 .env / SOUL.md:.env 含 WEIXIN_* 凭证由 channel bound 流管;
// SOUL.md 与 persona prompt 强相关,目前 manager 没有改 persona 的入口。
type hermesConfigRefresher struct {
	queries              hermesConfigQueries
	uploader             hermesConfigUploader
	cipher               *auth.Cipher
	knowledge            workerhandlers.KnowledgeReader
	newAPIBaseURL        string
	systemPromptTemplate string
}

func newHermesConfigRefresher(queries hermesConfigQueries, uploader hermesConfigUploader, cipher *auth.Cipher, knowledge workerhandlers.KnowledgeReader, newAPIBaseURL, systemPromptTemplate string) *hermesConfigRefresher {
	return &hermesConfigRefresher{
		queries:              queries,
		uploader:             uploader,
		cipher:               cipher,
		knowledge:            knowledge,
		newAPIBaseURL:        newAPIBaseURL,
		systemPromptTemplate: systemPromptTemplate,
	}
}

// RefreshConfigYAML 拿当前 app 状态 + 解密 token,渲染 config.yaml 并上传。
// 必备前提:app.NewapiKeyCiphertext 非空(由 app_initialize.ensureAPIKey 保证)。
func (r *hermesConfigRefresher) RefreshConfigYAML(ctx context.Context, appID string) error {
	if r.cipher == nil {
		return fmt.Errorf("hermesConfigRefresher.cipher 未配置,无法解密 OPENAI_API_KEY")
	}
	if r.uploader == nil {
		return fmt.Errorf("hermesConfigRefresher.uploader 未配置,无法上传 config.yaml")
	}
	id, err := uuid.Parse(appID)
	if err != nil {
		return fmt.Errorf("非法 app_id %q: %w", appID, err)
	}
	app, err := r.queries.GetApp(ctx, pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		return fmt.Errorf("查询应用失败: %w", err)
	}
	if !app.NewapiKeyCiphertext.Valid || app.NewapiKeyCiphertext.String == "" {
		return fmt.Errorf("应用 %s 未初始化 newapi_key_ciphertext,跳过 config.yaml 重写", appID)
	}
	token, err := r.cipher.Decrypt(app.NewapiKeyCiphertext.String)
	if err != nil {
		return fmt.Errorf("解密 OPENAI_API_KEY 失败: %w", err)
	}
	newAPIURL := r.newAPIBaseURL
	if strings.TrimSpace(newAPIURL) == "" {
		newAPIURL = "http://new-api:3000"
	}
	yamlContent, err := hermes.RenderConfigYAML(hermes.ConfigInput{
		ModelName:   app.ModelID,
		NewAPIURL:   newAPIURL,
		NewAPIToken: string(token),
	})
	if err != nil {
		return fmt.Errorf("渲染 config.yaml 失败: %w", err)
	}
	// RuntimeNodeID 在多节点部署下决定上传目标节点;若为空(单节点本地容器化)
	// 此 helper 也返回空串,UploadAppRuntimeFile 内部会走默认 fallback。
	nodeID := uuidToStringWiring(app.RuntimeNodeID)
	// 幂等地确保 .hermes/workspace 等关键子目录存在——存量 app 在升级到
	// "cwd=workspace"配置之前没建过 workspace 目录,restart 时补建避免
	// agent 首次 exec 时 cd 失败。InitAppDirs 内部 MkdirAll,已存在视为成功。
	if err := r.uploader.InitAppDirs(ctx, nodeID, appID); err != nil {
		return fmt.Errorf("确保 app 目录失败: %w", err)
	}
	if err := r.uploader.UploadAppRuntimeFile(ctx, nodeID, appID, "config.yaml", strings.NewReader(yamlContent)); err != nil {
		return fmt.Errorf("上传 config.yaml: %w", err)
	}
	// 重新渲染并上传组织/应用知识库 skills,使 restart 后容器内 skills 与
	// manager 主副本保持一致;主副本未变时上传的是相同字节,Hermes restart 后
	// 重新扫描即可生效(skill 加载在容器启动时一次性物化)。
	if err := r.refreshSkills(ctx, nodeID, appID, uuidToStringWiring(app.OrgID)); err != nil {
		return fmt.Errorf("刷新知识库 skills: %w", err)
	}
	// 重渲 SOUL.md,把最新知识库 inline 作为 always-on context。
	// Hermes 的 skills 是 progressive disclosure (skill_view 才装),
	// agent 不一定主动调,所以走 SOUL.md always-on 路径保证业务知识被读到。
	if err := r.refreshSoulMD(ctx, nodeID, app); err != nil {
		return fmt.Errorf("刷新 SOUL.md: %w", err)
	}
	return nil
}

// refreshSoulMD 在 restart 时根据当前 app/org/owner 重新渲染 SOUL.md,
// 末尾拼上组织 + 应用知识库的全部内容作为 always-on 业务上下文。
// 失败仅记错误,不阻塞 restart(prompt 全空 / org 缺失 / queries 失败时跳过)。
func (r *hermesConfigRefresher) refreshSoulMD(ctx context.Context, nodeID string, app sqlc.App) error {
	if r.uploader == nil {
		return fmt.Errorf("hermesConfigRefresher.uploader 未配置,无法上传 SOUL.md")
	}
	appID := uuidToStringWiring(app.ID)
	org, err := r.queries.GetOrganization(ctx, app.OrgID)
	if err != nil {
		return fmt.Errorf("查询组织失败: %w", err)
	}
	owner, err := r.queries.GetUser(ctx, app.OwnerUserID)
	if err != nil {
		return fmt.Errorf("查询应用 owner 失败: %w", err)
	}
	soulBody := ""
	promptResult, perr := hermes.Render(hermes.PromptInput{
		PlatformPrompt: r.systemPromptTemplate,
		OrgPrompt:      "",
		AppPrompt:      pgtypeText(app.AppPrompt),
		Variables:      hermes.VariablesFromContext(org.Name, app.Name, owner.DisplayName),
	})
	if perr == nil {
		soulBody = promptResult.Prompt
	}
	knowledgeInline, kerr := r.collectKnowledgeForSoul(uuidToStringWiring(app.OrgID), appID)
	if kerr != nil {
		return fmt.Errorf("拼接知识库到 SOUL.md 失败: %w", kerr)
	}
	if knowledgeInline != "" {
		if soulBody != "" {
			soulBody += "\n\n"
		}
		soulBody += knowledgeInline
	}
	if soulBody == "" {
		// SOUL.md 全空(无 prompt 也无知识库)时不上传,保留原 SOUL.md 不动。
		return nil
	}
	return r.uploader.UploadAppRuntimeFile(ctx, nodeID, appID, "SOUL.md", strings.NewReader(soulBody))
}

// pgtypeText 安全提取 pgtype.Text 内容;.Valid 为 false 时返回空串。
func pgtypeText(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}

// collectKnowledgeForSoul 与 worker handlers 端的实现等价:递归 org + app
// 知识库主副本,按"应用级在前、组织级在后"的顺序拼成 markdown 块,作为
// SOUL.md always-on 业务上下文。
func (r *hermesConfigRefresher) collectKnowledgeForSoul(orgID, appID string) (string, error) {
	if r.knowledge == nil {
		return "", nil
	}
	const perFileMax = 8 * 1024
	appPrefix := fmt.Sprintf("org/%s/app/%s/knowledge", orgID, appID)
	orgPrefix := fmt.Sprintf("org/%s/knowledge", orgID)

	type entry struct{ scope, relPath, body string }
	var entries []entry

	collect := func(scope, prefix string) error {
		return r.knowledge.WalkFiles(prefix, func(relPath string, _ int64) error {
			reader, _, err := r.knowledge.Open(prefix + "/" + relPath)
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

// refreshSkills 把组织 + 应用知识库主副本的所有文件渲染为 .hermes/skills/
// 下的 SKILL.md 并上传到容器。knowledge 未注入时直接跳过(保留旧装配兼容);
// 主副本目录不存在视为空集,不报错。
func (r *hermesConfigRefresher) refreshSkills(ctx context.Context, nodeID, appID, orgID string) error {
	if r.knowledge == nil {
		return nil
	}
	orgPrefix := fmt.Sprintf("org/%s/knowledge", orgID)
	if err := r.uploadSkillScope(ctx, nodeID, appID, hermes.ScopeOrg, orgPrefix); err != nil {
		return fmt.Errorf("写组织 skills 失败: %w", err)
	}
	appPrefix := fmt.Sprintf("org/%s/app/%s/knowledge", orgID, appID)
	if err := r.uploadSkillScope(ctx, nodeID, appID, hermes.ScopeApp, appPrefix); err != nil {
		return fmt.Errorf("写应用 skills 失败: %w", err)
	}
	return nil
}

// uploadSkillScope 遍历 prefix 下所有文件,渲染并上传成 SKILL.md。
// 与 worker handlers.uploadKnowledgeSkills 同构,共存于装配层避免反向依赖。
func (r *hermesConfigRefresher) uploadSkillScope(ctx context.Context, nodeID, appID string, scope hermes.SkillScope, prefix string) error {
	return r.knowledge.WalkFiles(prefix, func(relPath string, _ int64) error {
		master := prefix + "/" + relPath
		reader, _, err := r.knowledge.Open(master)
		if err != nil {
			return fmt.Errorf("打开主副本 %s 失败: %w", master, err)
		}
		body, readErr := io.ReadAll(reader)
		_ = reader.Close()
		if readErr != nil {
			return fmt.Errorf("读取主副本 %s 失败: %w", master, readErr)
		}
		slug := hermes.SlugifyKnowledgePath(relPath)
		rendered, err := hermes.RenderKnowledgeSkill(hermes.KnowledgeDoc{
			Scope: scope,
			Slug:  slug,
			Title: relPath,
			// Summary 拼成业务化引导文案,直接进 SKILL.md frontmatter description;
			// agent 按 description 选择性装载,这里要让它知道"组织/应用知识库,涉及业务必读"。
			Summary: hermes.BuildKnowledgeSummary(scope, relPath, string(body)),
			Body:    string(body),
		})
		if err != nil {
			return fmt.Errorf("渲染 SKILL.md %s 失败: %w", master, err)
		}
		target := fmt.Sprintf("skills/%s/SKILL.md", rendered.DirName)
		if err := r.uploader.UploadAppRuntimeFile(ctx, nodeID, appID, target, strings.NewReader(rendered.SkillMD)); err != nil {
			return fmt.Errorf("上传 %s 失败: %w", target, err)
		}
		return nil
	})
}

// lookupNode 同时返回节点行与 agent token；任何字段缺失立即报错让上层快速失败。
func (n *nodeClientResolver) lookupNode(ctx context.Context, nodeID string) (sqlc.RuntimeNode, string, error) {
	if nodeID == "" {
		return sqlc.RuntimeNode{}, "", fmt.Errorf("nodeID 不能为空")
	}
	id, err := parseUUIDForWiring(nodeID)
	if err != nil {
		return sqlc.RuntimeNode{}, "", fmt.Errorf("非法 nodeID %s: %w", nodeID, err)
	}
	node, err := n.queries.GetRuntimeNode(ctx, id)
	if err != nil {
		return sqlc.RuntimeNode{}, "", fmt.Errorf("查询节点 %s 失败: %w", nodeID, err)
	}
	token, err := n.tokens.Get(nodeID)
	if err != nil {
		return sqlc.RuntimeNode{}, "", fmt.Errorf("节点 %s 的 agent token 不可用（需要重启 agent 触发自动注册）: %w", nodeID, err)
	}
	return node, token, nil
}

// agentHTTPClient 按节点 TLS CA 构建 agent HTTP client。
// timeout 为 0 时不设 http.Client.Timeout，由调用方 ctx 控制截止时间；
// 普通文件 API 传 30s，大流式上传（镜像 load）传 0。
func (n *nodeClientResolver) agentHTTPClient(node sqlc.RuntimeNode, timeout time.Duration) (*http.Client, error) {
	if !node.AgentTlsCaCert.Valid || strings.TrimSpace(node.AgentTlsCaCert.String) == "" {
		return nil, fmt.Errorf("节点 %s 缺 agent_tls_ca_cert", uuidToStringWiring(node.ID))
	}
	pool, err := agent.BuildCertPool(node.AgentTlsCaCert.String)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs:    pool,
			MinVersion: tls.VersionTLS12,
		}},
	}, nil
}

// appContainerLookup 实现 channel.AppContainerLookup，通过 sqlc.Queries 取容器 ID。
type appContainerLookup struct {
	queries appLookupQueries
}

type appLookupQueries interface {
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
}

func newAppContainerLookup(queries appLookupQueries) *appContainerLookup {
	return &appContainerLookup{queries: queries}
}

// LookupContainer 按 appID 取容器 ID。
// app 不存在或 container_id 为空时返回错误，让 wechat runner 立刻冒泡。
func (l *appContainerLookup) LookupContainer(ctx context.Context, appID string) (string, error) {
	id, err := parseUUIDForWiring(appID)
	if err != nil {
		return "", fmt.Errorf("非法 appID %s: %w", appID, err)
	}
	app, err := l.queries.GetApp(ctx, id)
	if err != nil {
		return "", fmt.Errorf("查询应用 %s 失败: %w", appID, err)
	}
	if !app.ContainerID.Valid || app.ContainerID.String == "" {
		return "", fmt.Errorf("应用 %s 尚未创建容器", appID)
	}
	return app.ContainerID.String, nil
}

// parseUUIDForWiring 复用为 wiring 内部使用，避免与 service 包的 parseUUID 互引导致循环依赖。
func parseUUIDForWiring(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		return pgtype.UUID{}, err
	}
	return id, nil
}

// runtimeInspectorWrapper 把 runtime.Adapter.InspectContainer 适配成 service.RuntimeInspector。
// service 层只声明最小接口形态，wrapper 在 cmd/server 把 runtime 包的具体类型翻译过去。
type runtimeInspectorWrapper struct {
	adapter inspectingAdapter
}

// inspectingAdapter 描述 runtime.Adapter 中我们用到的 InspectContainer 子集。
type inspectingAdapter interface {
	InspectContainer(ctx context.Context, nodeID, containerID string) (runtime.ContainerInfo, error)
}

func newRuntimeInspectorWrapper(adapter inspectingAdapter) *runtimeInspectorWrapper {
	return &runtimeInspectorWrapper{adapter: adapter}
}

// InspectContainer 实现 service.RuntimeInspector，把 runtime.ContainerInfo 转换为
// service 层的视图字段。
func (w *runtimeInspectorWrapper) InspectContainer(ctx context.Context, nodeID, containerID string) (service.RuntimeContainerInfo, error) {
	info, err := w.adapter.InspectContainer(ctx, nodeID, containerID)
	if err != nil {
		return service.RuntimeContainerInfo{}, err
	}
	return service.RuntimeContainerInfo{
		ID:     info.ID,
		Name:   info.Name,
		Image:  info.Image,
		Status: info.Status,
	}, nil
}

// knowledgeSyncDispatcher 实现 service.KnowledgeSyncDispatcher：
// 把 manager 主副本写入事件按节点拆成 knowledge_sync_node job，并即时通知 Redis。
//
// 路由策略：
//   - org 维度：枚举 active 节点，全部同步（Phase A1 已知妥协，B 阶段后续可换 tar 全量）；
//   - app 维度：仅同步该应用所在节点。
//
// 同步入队完成后,还会通过 reloader 给受影响的运行中 app 入一条 debounced
// app_restart_container job:Hermes 容器只在启动时读 SOUL.md / skills,主副本
// 同步到节点目录后仍需重启容器才能让 LLM 真正读到新内容。
//
// 任意节点查询失败/job 写入失败立即冒泡到 service 层；service 层把错误写 audit_logs
// 后仍返回主流程 nil（主副本已经写入，不应因为同步失败让用户接口翻 500）。
type knowledgeSyncDispatcher struct {
	queries    knowledgeJobsQueries
	notifier   service.JobNotifier
	syncStatus knowledgeSyncStatusMarker
	// knowledge 用于 RetryOrgNode 全量重推时扫主副本所有文件并逐个入 upload_file job。
	// 也用于 EnqueueOrgReload 之前判断主副本是否为空(空目录无需重启 app)——可选优化,
	// 当前实现不做此判断:reload 是 idempotent 的,空主副本重启一次最多让 hermes 再读一遍空目录。
	knowledge workerhandlers.KnowledgeReader
	// reloader 负责给运行中 app 入 app_restart_container job(带 in-memory debounce),
	// 让 hermes 容器在重启后读到最新主副本。nil 时跳过 reload(测试装配兼容)。
	reloader knowledgeAppReloader
}

type knowledgeJobsQueries interface {
	ListRuntimeNodes(ctx context.Context, arg sqlc.ListRuntimeNodesParams) ([]sqlc.RuntimeNode, error)
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	ListAppsByOrg(ctx context.Context, arg sqlc.ListAppsByOrgParams) ([]sqlc.App, error)
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error)
}

// knowledgeAppReloader 抽象「让目标 app(s) 重启以加载新知识库」能力。
// 实现需要做 in-memory debounce(同一 app 在 window 内只入一次 restart job),
// 否则 N 次连续上传会引发 N 次容器重启,严重影响用户体验。
type knowledgeAppReloader interface {
	// EnqueueAppReload 给单个 app 入 reload job;非 running/binding_waiting 状态跳过。
	EnqueueAppReload(ctx context.Context, appID string) error
	// EnqueueOrgReload 列出该 org 所有 running app 并逐个入 reload job。
	EnqueueOrgReload(ctx context.Context, orgID string) error
}

// knowledgeSyncStatusMarker 抽象 (org, node) 状态写入；与 worker handler 同的接口
// 让 dispatcher 入队时把 pending 写入 knowledge_sync_status，前端能立刻看到"待同步"。
type knowledgeSyncStatusMarker interface {
	MarkOrgNodePending(ctx context.Context, orgID, nodeID string) error
}

func newKnowledgeSyncDispatcher(queries knowledgeJobsQueries, notifier service.JobNotifier) *knowledgeSyncDispatcher {
	return &knowledgeSyncDispatcher{queries: queries, notifier: notifier}
}

// SetStatusMarker 注入状态写入器；不调时 dispatcher 不写 pending（旧装配兼容）。
func (d *knowledgeSyncDispatcher) SetStatusMarker(m knowledgeSyncStatusMarker) {
	d.syncStatus = m
}

// SetKnowledgeReader 注入主副本读取能力。RetryOrgNode 用它扫主副本所有文件,
// 把"重试同步"真的变成全量重推(原 noop 实现只翻状态不动文件)。
func (d *knowledgeSyncDispatcher) SetKnowledgeReader(r workerhandlers.KnowledgeReader) {
	d.knowledge = r
}

// SetReloader 注入 app reload 入队器。注入后任何 org / app 知识库改动都会触发
// 受影响 app 的容器重启,使 Hermes 真正读到新 SOUL.md / skills;不注入则 hermes
// 容器内的内容停留在 app_initialize 时的快照,与主副本失同步。
func (d *knowledgeSyncDispatcher) SetReloader(r knowledgeAppReloader) {
	d.reloader = r
}

// RetryOrgNode 触发指定 (org, node) 重新同步:扫主副本 org/<id>/knowledge 下所有
// 文件,每个文件单独入一条 upload_file sync job,worker 逐个推到目标节点,完成后
// status_writer 把状态翻回 synced。
//
// 历史:原实现入 noop job 直接翻 synced,但实际文件并未重推——遇到节点上文件
// 损坏或丢失时,"重试同步"按钮按完状态变 synced 但内容照旧,完全无法修复。
// 现在改为字面意义上的"全量重新同步"。
//
// 空目录或 knowledge reader 未注入(测试装配)时退化为入一条 noop,让 status 推到
// synced(没有内容要同步,语义上也是"最新")。
func (d *knowledgeSyncDispatcher) RetryOrgNode(ctx context.Context, orgID, nodeID string) error {
	// pending 状态写入失败不阻塞主链路;worker 完成时会再次 upsert(synced/failed)。
	if d.syncStatus != nil {
		_ = d.syncStatus.MarkOrgNodePending(ctx, orgID, nodeID)
	}
	if d.knowledge == nil {
		// 装配未注入 KnowledgeReader 时退化:保留旧 noop 行为,让 worker mark synced。
		return d.enqueue(ctx, knowledgeSyncJobInput{
			Scope: "org", OrgID: orgID, NodeID: nodeID,
			ChangeType: "noop", RelPath: "(retry)", MasterPath: "(retry)",
		})
	}
	prefix := fmt.Sprintf("org/%s/knowledge", orgID)
	enqueued := 0
	walkErr := d.knowledge.WalkFiles(prefix, func(relPath string, _ int64) error {
		if err := d.enqueue(ctx, knowledgeSyncJobInput{
			Scope:      "org",
			OrgID:      orgID,
			NodeID:     nodeID,
			ChangeType: "upload_file",
			RelPath:    relPath,
			MasterPath: prefix + "/" + relPath,
		}); err != nil {
			return err
		}
		enqueued++
		return nil
	})
	if walkErr != nil {
		return fmt.Errorf("扫主副本失败: %w", walkErr)
	}
	if enqueued == 0 {
		// 主副本为空——退化到 noop 让 worker mark synced,避免"重试按完前端永远 pending"。
		return d.enqueue(ctx, knowledgeSyncJobInput{
			Scope: "org", OrgID: orgID, NodeID: nodeID,
			ChangeType: "noop", RelPath: "(retry-empty)", MasterPath: "(retry-empty)",
		})
	}
	return nil
}

// DispatchOrgChange 给所有 active 节点入队一个 sync 任务。
// 入队成功后立刻写 (org, node) = pending 状态，让前端立即可见"同步中"。
// 最后再让 reloader 把该 org 所有 running app 排进 debounced 重启,使 Hermes 容器
// 真正读到新主副本(SOUL.md / skills 仅在容器启动时加载)。
func (d *knowledgeSyncDispatcher) DispatchOrgChange(ctx context.Context, orgID, relPath, changeType, masterPath string) error {
	nodes, err := d.queries.ListRuntimeNodes(ctx, sqlc.ListRuntimeNodesParams{Limit: 200, Offset: 0})
	if err != nil {
		return fmt.Errorf("查询节点失败: %w", err)
	}
	for _, node := range nodes {
		if node.Status != "active" {
			continue
		}
		nodeID := uuidToStringWiring(node.ID)
		if err := d.enqueue(ctx, knowledgeSyncJobInput{
			Scope:      "org",
			OrgID:      orgID,
			NodeID:     nodeID,
			ChangeType: changeType,
			RelPath:    relPath,
			MasterPath: masterPath,
		}); err != nil {
			return err
		}
		// pending 状态写入失败不阻塞主链路：worker 完成时会再次 upsert（synced/failed）。
		if d.syncStatus != nil {
			_ = d.syncStatus.MarkOrgNodePending(ctx, orgID, nodeID)
		}
	}
	// 入队 reload:reload 是"附属操作",失败仅记日志,不让用户的上传/删除接口翻 500。
	// 让 hermes 真读到新内容必须经过容器重启(SOUL.md / skills 启动时 snapshot)。
	if d.reloader != nil {
		if err := d.reloader.EnqueueOrgReload(ctx, orgID); err != nil {
			slog.WarnContext(ctx, "入队 org reload 失败", "org_id", orgID, "error", err)
		}
	}
	return nil
}

// DispatchAppChange 给应用所在节点入队 sync 任务,并对该 app 入 debounced reload。
func (d *knowledgeSyncDispatcher) DispatchAppChange(ctx context.Context, orgID, appID, relPath, changeType, masterPath string) error {
	id, err := parseUUIDForWiring(appID)
	if err != nil {
		return err
	}
	app, err := d.queries.GetApp(ctx, id)
	if err != nil {
		return fmt.Errorf("查询应用失败: %w", err)
	}
	if !app.RuntimeNodeID.Valid {
		return nil // 应用未绑定节点，跳过
	}
	if err := d.enqueue(ctx, knowledgeSyncJobInput{
		Scope:      "app",
		OrgID:      orgID,
		AppID:      appID,
		NodeID:     uuidToStringWiring(app.RuntimeNodeID),
		ChangeType: changeType,
		RelPath:    relPath,
		MasterPath: masterPath,
	}); err != nil {
		return err
	}
	if d.reloader != nil {
		if err := d.reloader.EnqueueAppReload(ctx, appID); err != nil {
			slog.WarnContext(ctx, "入队 app reload 失败", "app_id", appID, "error", err)
		}
	}
	return nil
}

type knowledgeSyncJobInput struct {
	// Scope 区分 org/app 同步范围，worker 依此选择目标知识库目录。
	Scope string
	// OrgID 是知识库同步的组织边界，所有 job 都必须携带。
	OrgID string
	// AppID 仅在 Scope=app 时有效，用于定位应用知识库目录。
	AppID string
	// NodeID 是目标 runtime node，dispatcher 已在入队前完成路由选择。
	NodeID string
	// ChangeType 表示 upload/delete/noop，worker 依此选择同步动作。
	ChangeType string
	// RelPath 是相对知识库根目录的安全路径，不能直接当宿主机绝对路径使用。
	RelPath string
	// MasterPath 是 manager 主副本中的本地文件路径；worker 从本地读取后再通过 agent API 写入节点。
	MasterPath string
}

// knowledgeReloadCoordinator 实现 knowledgeAppReloader:给运行中 app 入
// app_restart_container job,让 Hermes 容器重启后读到新主副本。
//
// 关键设计点:
//   - in-memory debounce:同一 appID 在 window 内只入一次 reload job。原因是
//     用户连续上传 N 个文件 → DispatchAppChange 触发 N 次 reload → N 次重启,
//     每次 5-10s 用户感知到 hermes 长时间不可用。debounce 把"短时间内的多次改动"
//     合并为一次"延迟重启",在最后一次改动 ~delay 秒后才实际重启容器。
//   - RunAfter=now+delay:让 worker 在 delay 秒后才取 job,给后续的连续改动
//     一个时间窗口落到同一次重启。
//   - 仅 running / binding_waiting 才入:其它状态(init/stopped/failed/deleted)
//     重启没有业务意义,入了反而让 worker 跑空。
//
// 进程级状态:manager 单实例部署时 in-memory map 足够;若未来横向扩展为
// 多个 manager 实例,需要换成 redis SETNX。当前 debounce 失效带来的代价
// 是"多重启一次",不会引发数据问题。
type knowledgeReloadCoordinator struct {
	queries  knowledgeReloadQueries
	notifier service.JobNotifier
	mu       sync.Mutex
	lastAt   map[string]time.Time
	window   time.Duration // 同一 appID 多次入队的抑制窗口
	delay    time.Duration // 入队时 RunAfter 相对 now 的偏移
}

// knowledgeReloadQueries 是 reload 协调器用到的 sqlc 子集。
type knowledgeReloadQueries interface {
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	ListAppsByOrg(ctx context.Context, arg sqlc.ListAppsByOrgParams) ([]sqlc.App, error)
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error)
}

// newKnowledgeReloadCoordinator 创建协调器,window / delay 取经验值 5s。
//
// 5s 的权衡:太短(<2s)用户连续上传多个文件时无法合并重启;太长(>30s)用户
// 在前端等"刚才上传是否生效"的时间过久。5s 在两者之间,与 docker 容器健康
// 检查的典型周期同量级。
func newKnowledgeReloadCoordinator(queries knowledgeReloadQueries, notifier service.JobNotifier) *knowledgeReloadCoordinator {
	return &knowledgeReloadCoordinator{
		queries:  queries,
		notifier: notifier,
		lastAt:   map[string]time.Time{},
		window:   5 * time.Second,
		delay:    5 * time.Second,
	}
}

// tryDebounce 在 window 内返回 false 抑制本次入队;否则更新 lastAt 并返回 true。
func (c *knowledgeReloadCoordinator) tryDebounce(appID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if last, ok := c.lastAt[appID]; ok && now.Sub(last) < c.window {
		return false
	}
	c.lastAt[appID] = now
	return true
}

// EnqueueAppReload 给单个 app 入 app_restart_container job(若 app 处于 running /
// binding_waiting 状态)。其它状态直接返回 nil(不算错,只是没必要重启)。
func (c *knowledgeReloadCoordinator) EnqueueAppReload(ctx context.Context, appID string) error {
	id, err := parseUUIDForWiring(appID)
	if err != nil {
		return fmt.Errorf("非法 app_id: %w", err)
	}
	app, err := c.queries.GetApp(ctx, id)
	if err != nil {
		return fmt.Errorf("查询应用失败: %w", err)
	}
	return c.enqueueIfReloadable(ctx, app)
}

// EnqueueOrgReload 列该 org 所有 active app,逐个调 enqueueIfReloadable。
// 单个 app 入队失败不中断其他 app(reload 是 best-effort);最先发生的错误
// 会作为返回值,调用方决定是否记 warn。
func (c *knowledgeReloadCoordinator) EnqueueOrgReload(ctx context.Context, orgID string) error {
	id, err := parseUUIDForWiring(orgID)
	if err != nil {
		return fmt.Errorf("非法 org_id: %w", err)
	}
	// limit=500:单组织 app 数量极少超过此规模;若超过则后续 page 不重启,
	// 由 user 手动重启或下次改动自然触发。避免无界查询导致 manager 内存压力。
	apps, err := c.queries.ListAppsByOrg(ctx, sqlc.ListAppsByOrgParams{OrgID: id, Limit: 500, Offset: 0})
	if err != nil {
		return fmt.Errorf("列出组织应用失败: %w", err)
	}
	var firstErr error
	for _, app := range apps {
		if err := c.enqueueIfReloadable(ctx, app); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// enqueueIfReloadable 共享逻辑:状态过滤 + debounce + 入 restart job。
func (c *knowledgeReloadCoordinator) enqueueIfReloadable(ctx context.Context, app sqlc.App) error {
	if app.Status != domain.AppStatusRunning && app.Status != domain.AppStatusBindingWaiting {
		return nil
	}
	appID := uuidToStringWiring(app.ID)
	if !c.tryDebounce(appID) {
		return nil
	}
	payload, err := jsonMarshal(map[string]any{
		"app_id":       appID,
		"operation":    string(service.RuntimeOperationRestart),
		"runtime_node": uuidToStringWiring(app.RuntimeNodeID),
		"trigger":      "knowledge_reload", // 仅元数据,worker handler 不读;留作 audit/排障
	})
	if err != nil {
		return fmt.Errorf("序列化 reload payload 失败: %w", err)
	}
	job, err := c.queries.CreateJob(ctx, sqlc.CreateJobParams{
		Type:        domain.JobTypeAppRestartContainer,
		Priority:    50,
		MaxAttempts: 3,
		// RunAfter 延后 delay 秒:给后续可能的连续改动一个合并窗口。
		// worker 在该时刻之后才会取这条 job。
		RunAfter:    pgtype.Timestamptz{Time: time.Now().Add(c.delay), Valid: true},
		PayloadJson: payload,
	})
	if err != nil {
		return fmt.Errorf("入队 reload job 失败: %w", err)
	}
	if c.notifier != nil {
		_ = c.notifier.Enqueue(ctx, uuidToStringWiring(job.ID))
	}
	return nil
}

func (d *knowledgeSyncDispatcher) enqueue(ctx context.Context, input knowledgeSyncJobInput) error {
	// payload 字段名是 worker handler 的契约，不能随意改名，否则旧任务会无法解析。
	payload := map[string]any{
		"scope":       input.Scope,
		"org_id":      input.OrgID,
		"app_id":      input.AppID,
		"node_id":     input.NodeID,
		"change_type": input.ChangeType,
		"rel_path":    input.RelPath,
		"master_path": input.MasterPath,
	}
	body, err := jsonMarshal(payload)
	if err != nil {
		return err
	}
	job, err := d.queries.CreateJob(ctx, sqlc.CreateJobParams{
		Type:        "knowledge_sync_node",
		Priority:    50,
		MaxAttempts: 5,
		RunAfter:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
		PayloadJson: body,
	})
	if err != nil {
		return fmt.Errorf("创建 sync job 失败: %w", err)
	}
	if d.notifier != nil {
		_ = d.notifier.Enqueue(ctx, uuidToStringWiring(job.ID))
	}
	return nil
}

// uuidToStringWiring 把 pgtype.UUID 转 16 位标准字符串；与 service 层的 uuidToString 等价，
// 这里复制是为了避免 wiring → service 的反向引用。
func uuidToStringWiring(id pgtype.UUID) string {
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

// jsonMarshal 是 cmd/server 内部 json.Marshal 的简短封装，便于 dispatcher 复用。
var jsonMarshal = json.Marshal

// runtimeRefreshJobsQueries 是 runtimeRefreshDispatcher 用到的 sqlc 子集。
type runtimeRefreshJobsQueries interface {
	ListRunningApps(ctx context.Context) ([]sqlc.ListRunningAppsRow, error)
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error)
}

// runtimeRefreshDispatcher 周期扫描 status in (running, binding_waiting) 应用，
// 对每个入队一条 runtime_refresh_status job。worker handler 写 apps.runtime_snapshot_json，
// 前端 AppRuntimeTab 拉这一列展示资源占用。
//
// 间隔由 main.go PeriodicReconciler 的 30s 控制；ListRunningApps 自身只读，
// 重复入队相同 job 是幂等的（worker 拿到的是最新 inspect 结果）。
type runtimeRefreshDispatcher struct {
	queries  runtimeRefreshJobsQueries
	notifier service.JobNotifier
}

func newRuntimeRefreshDispatcher(queries runtimeRefreshJobsQueries, notifier service.JobNotifier) *runtimeRefreshDispatcher {
	return &runtimeRefreshDispatcher{queries: queries, notifier: notifier}
}

// Tick 列出待刷新应用并入队 runtime_refresh_status job；任一应用失败不阻断其他应用。
func (d *runtimeRefreshDispatcher) Tick(ctx context.Context) error {
	return enqueuePerRunningApp(ctx, d.queries, d.notifier, domain.JobTypeRuntimeRefreshStatus, 20, 1)
}

// healthCheckDispatcher 周期入队 app_health_check job：复用 runtimeRefreshJobsQueries
// 与 enqueuePerRunningApp helper，差异只在 job 类型与优先级。
type healthCheckDispatcher struct {
	queries  runtimeRefreshJobsQueries
	notifier service.JobNotifier
}

func newHealthCheckDispatcher(queries runtimeRefreshJobsQueries, notifier service.JobNotifier) *healthCheckDispatcher {
	return &healthCheckDispatcher{queries: queries, notifier: notifier}
}

// Tick 列出需要探活的应用并入队 app_health_check job。
func (d *healthCheckDispatcher) Tick(ctx context.Context) error {
	return enqueuePerRunningApp(ctx, d.queries, d.notifier, domain.JobTypeAppHealthCheck, 30, 1)
}

// enqueuePerRunningApp 是 runtime_refresh_status 与 app_health_check 共用的扫描入队逻辑。
// 任一应用 CreateJob 失败 continue 不阻断；返回错误仅在 ListRunningApps 失败时。
func enqueuePerRunningApp(ctx context.Context, queries runtimeRefreshJobsQueries, notifier service.JobNotifier, jobType string, priority int32, maxAttempts int32) error {
	rows, err := queries.ListRunningApps(ctx)
	if err != nil {
		return fmt.Errorf("列出 running 应用失败: %w", err)
	}
	for _, row := range rows {
		appID := uuidToStringWiring(row.ID)
		payload, err := jsonMarshal(map[string]any{"app_id": appID})
		if err != nil {
			continue
		}
		job, err := queries.CreateJob(ctx, sqlc.CreateJobParams{
			Type:        jobType,
			Priority:    priority,
			MaxAttempts: maxAttempts,
			RunAfter:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
			PayloadJson: payload,
		})
		if err != nil {
			continue
		}
		if notifier != nil {
			_ = notifier.Enqueue(ctx, uuidToStringWiring(job.ID))
		}
	}
	return nil
}

// persistentTokenLoader 适配 store.AgentTokenStore 实现 agent.PersistentTokenLoader。
// cache miss 时从数据库读密文 → cipher.Decrypt 还原明文 → 由 TokenResolver 回填 cache。
type persistentTokenLoader struct {
	store  *store.AgentTokenStore
	cipher *auth.Cipher
}

func newPersistentTokenLoader(s *store.AgentTokenStore, c *auth.Cipher) *persistentTokenLoader {
	return &persistentTokenLoader{store: s, cipher: c}
}

// LoadAgentToken 实现 agent.PersistentTokenLoader。
// 任何失败（节点不存在、密文损坏、解密失败）都返回错误；调用方据此返回 401 让 agent 重新注册。
func (l *persistentTokenLoader) LoadAgentToken(ctx context.Context, nodeID string) (string, error) {
	if l.store == nil || l.cipher == nil {
		return "", nil
	}
	id, err := parseUUIDForWiring(nodeID)
	if err != nil {
		return "", err
	}
	ciphertext, err := l.store.Get(ctx, id)
	if err != nil {
		return "", fmt.Errorf("查询 agent token 密文失败: %w", err)
	}
	if ciphertext == "" {
		return "", nil
	}
	plain, err := l.cipher.Decrypt(ciphertext)
	if err != nil {
		return "", fmt.Errorf("解密 agent token 失败: %w", err)
	}
	return string(plain), nil
}

// persistAgentToken 把 agent token 加密后写入数据库。
// 加密失败不冒泡：成功的 enroll 响应已经返回给 agent，持久化失败只走日志。
func persistAgentToken(ctx context.Context, s *store.AgentTokenStore, c *auth.Cipher, nodeID, token string) error {
	if s == nil || c == nil {
		return nil
	}
	id, err := parseUUIDForWiring(nodeID)
	if err != nil {
		return err
	}
	ciphertext, err := c.Encrypt([]byte(token))
	if err != nil {
		return fmt.Errorf("加密 agent token 失败: %w", err)
	}
	return s.Set(ctx, id, ciphertext)
}

// appDirInitializerAdapter 把 *runtime.AgentBackedAdapter 适配成
// handlers.AgentDirInitializer，仅暴露 InitAppDirs 一个方法，避免 handler 依赖
// 整个 adapter 类型导致测试 mock 复杂。生产装配传 runtimeAdapter 即可。
type appDirInitializerAdapter struct {
	adapter interface {
		InitAppDirs(ctx context.Context, nodeID, appID string) error
	}
}

// InitAppDirs 仅透传应用目录初始化调用，保持 handler 只依赖最小接口。
func (a appDirInitializerAdapter) InitAppDirs(ctx context.Context, nodeID, appID string) error {
	return a.adapter.InitAppDirs(ctx, nodeID, appID)
}

// orgCredentialsRefresher 是 newapi.CredentialsRefresher 的实现。
//
// 一个 refresher 实例绑定单个组织 + cipher + base client。RefreshAccessToken：
//  1. SELECT ... FOR UPDATE 锁住该组织行；
//  2. 解密密文取 password；
//  3. 调 BootstrapUserAccessToken 拿新 access_token；
//  4. 加密 {username, password, new_access_token} → UpdateOrganizationCredentialsCiphertext；
//  5. 返回新 access_token。
//
// 第一版没有事务包装：FOR UPDATE 在隐式自动提交场景下退化为普通 SELECT。
type orgCredentialsRefresher struct {
	// store 用于读取/写回组织凭据密文。
	store *sqlc.Queries
	// cipher 用于解密旧凭据和加密新 access_token。
	cipher *auth.Cipher
	// client 是 admin/base 视角 new-api client，用于重新换取组织用户 access_token。
	client *newapi.Client
	// orgID 标识当前刷新器绑定的组织，避免跨组织写回凭据。
	orgID pgtype.UUID
	// username/password 是组织在 new-api 中的登录凭据，只保留在内存中用于刷新 access_token。
	username string
	password string
}

// RefreshAccessToken 刷新组织在 new-api 中的 access_token 并写回密文凭据。
func (r *orgCredentialsRefresher) RefreshAccessToken(ctx context.Context) (string, error) {
	org, err := r.store.GetOrganizationForUpdate(ctx, r.orgID)
	if err != nil {
		return "", fmt.Errorf("RefreshAccessToken 锁组织失败: %w", err)
	}
	newToken, err := r.client.BootstrapUserAccessToken(ctx, r.username, r.password)
	if err != nil {
		return "", fmt.Errorf("RefreshAccessToken 重新登录失败: %w", err)
	}
	payload, err := json.Marshal(service.OrganizationCredentials{
		Username:    r.username,
		Password:    r.password,
		AccessToken: newToken,
	})
	if err != nil {
		return "", err
	}
	ciphertext, err := r.cipher.Encrypt(payload)
	if err != nil {
		return "", err
	}
	_, err = r.store.UpdateOrganizationCredentialsCiphertext(ctx, sqlc.UpdateOrganizationCredentialsCiphertextParams{
		ID:                              org.ID,
		NewapiUserCredentialsCiphertext: pgtype.Text{String: ciphertext, Valid: true},
	})
	if err != nil {
		return "", fmt.Errorf("RefreshAccessToken 写回密文失败: %w", err)
	}
	return newToken, nil
}

// orgScopedClientFactory 把 sqlc 组织行 + manager cipher + newapi.Client 组合成
// handlers.NewAPIClientFactory：worker handler 在跑 job 时只需要把 sqlc.App 给到
// UserScopedFor，由 factory 反查组织凭据 → 解密 → 构造 user-scoped client，避免
// 每个 handler 都重复实现"读 organizations + 解 ciphertext"的样板。
type orgScopedClientFactory struct {
	client *newapi.Client
	store  *sqlc.Queries
	cipher *auth.Cipher
}

// UserScopedFor 解密组织凭据并返回以业务 user 身份调 token 操作的 client view。
//
// 调用前置条件：
//   - app.OrgID 必须已经存在；
//   - 该组织必须已经走过 OrganizationService.CreateOrganization 把 newapi_user_id
//     与 newapi_user_credentials_ciphertext 写齐；缺任意一项视作"未 provision"，立即报错。
func (f *orgScopedClientFactory) UserScopedFor(ctx context.Context, app sqlc.App) (workerhandlers.APIKeyClient, error) {
	if f.client == nil {
		return nil, fmt.Errorf("orgScopedClientFactory: newapi client 未配置")
	}
	if f.cipher == nil {
		return nil, fmt.Errorf("orgScopedClientFactory: cipher 未配置")
	}
	org, err := f.store.GetOrganization(ctx, app.OrgID)
	if err != nil {
		return nil, fmt.Errorf("查询组织失败: %w", err)
	}
	creds, err := service.DecryptOrganizationCredentials(org, f.cipher)
	if err != nil {
		return nil, err
	}
	if !org.NewapiUserID.Valid || org.NewapiUserID.String == "" {
		return nil, fmt.Errorf("组织 %s 未持有 new-api 用户 id", uuidToWiringString(org.ID))
	}
	userID, err := parseInt64ForWiring(org.NewapiUserID.String)
	if err != nil {
		return nil, fmt.Errorf("解析 newapi_user_id 失败: %w", err)
	}
	refresher := &orgCredentialsRefresher{
		store:    f.store,
		cipher:   f.cipher,
		client:   f.client,
		orgID:    org.ID,
		username: creds.Username,
		password: creds.Password,
	}
	return f.client.AsUserWithRefresh(userID, creds.AccessToken, refresher), nil
}

// parseInt64ForWiring 是 cmd/server 内部的小工具：把 string 解为 int64，error 直传。
// service 包里有同语义函数，但 wiring 层不便引入服务包内部 helper，复制一份避免循环依赖。
func parseInt64ForWiring(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

// uuidToWiringString 把 pgtype.UUID 渲染成可读字符串供错误信息使用。
// service 包里有同名 helper，wiring 层独立一份避免暴露 service 内部 API。
func uuidToWiringString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}
