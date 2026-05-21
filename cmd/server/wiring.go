// 装配 manager 进程所需的所有跨包依赖。
// 这里只放"接口适配 + 多节点路由"等胶水逻辑；具体业务规则由 service 层负责。
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
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

// appInputRefresherQueries 是 appInputRefresher 需要的最小 DB 查询子集。
// 抽接口便于单测注入内存桩, 不必引入完整 *sqlc.Queries 依赖。
type appInputRefresherQueries interface {
	GetOrganization(ctx context.Context, id pgtype.UUID) (sqlc.Organization, error)
	GetUser(ctx context.Context, id pgtype.UUID) (sqlc.User, error)
	GetAssistantVersion(ctx context.Context, id pgtype.UUID) (sqlc.AssistantVersion, error)
}

// appInputRefresher 实现 workerhandlers.AppInputRefresher,
// 在 app_restart_container job 真正调 stop/start 之前, 把节点上的
// apps/<id>/input/manifest.yaml + resources/*.md 重写成 DB 当前版本快照。
//
// 与 AppInitializeHandler.writeAppInput 共用 workerhandlers.AssembleVersionInputData
// 装配版本数据（routing / skill tar 推送），确保 init 与 restart 两条链路写出的
// manifest 版本字段完全一致，不会出现"初始化看得见、restart 后字段漂移"的问题。
//
// RefreshAppInput 成功后返回 AppInputRefreshResult，包含版本修订与镜像 ref，
// 供 restart handler 写入 apps.applied_version_revision / applied_image_ref。
type appInputRefresher struct {
	// queries 取 organization + owner + assistant_version 上下文。app 由 handler
	// 传入时已经是 GetApp 拿到的最新行，不需要再查一次，减少一次冗余 IO。
	queries appInputRefresherQueries
	// uploader 是按 nodeID 路由的应用 input 文件上传能力 (生产装配
	// 由 runtime.AgentBackedAdapter 提供, 内部转发到目标节点 agent file API)。
	uploader workerhandlers.AppInputUploader
	// cipher 用于解密 apps.newapi_key_ciphertext 取出 sk- 明文。
	// nil 时 RefreshAppInput 直接报错: 没法解密就无法写入正确的 manifest.credentials。
	cipher *auth.Cipher
	// resolveImage 把版本 image_id 解析为完整 imageRef（含 tag）。
	// nil 时 RefreshAppInput 直接报错，无法确定运行时镜像 ref。
	resolveImage func(imageID string) (string, bool)
	// skillBlobs 提供版本 skill tar 主副本的读能力，用于推送 skill 到节点 input/resources/skills/。
	// nil 时跳过 skill 推送（测试/旧装配兼容）。
	skillBlobs workerhandlers.SkillBlobReader
	// opts 携带 PlatformPrompt / NewAPIBaseURL / DefaultModel 兜底配置。
	// BuildAppInputData 根据它构造 hermes.AppInputData。
	opts workerhandlers.AppInputBuildOptions
}

// newAppInputRefresher 构造生产装配用的 refresher。
// uploader / cipher / resolveImage 任一为 nil 都允许 (调用 RefreshAppInput 时再报错),
// 保留与现有 wiring 一致的"未配某依赖就跳过"语义, 避免启动失败影响其他无关功能。
func newAppInputRefresher(queries appInputRefresherQueries, uploader workerhandlers.AppInputUploader, cipher *auth.Cipher, resolveImage func(string) (string, bool), skillBlobs workerhandlers.SkillBlobReader, opts workerhandlers.AppInputBuildOptions) *appInputRefresher {
	return &appInputRefresher{
		queries:      queries,
		uploader:     uploader,
		cipher:       cipher,
		resolveImage: resolveImage,
		skillBlobs:   skillBlobs,
		opts:         opts,
	}
}

// RefreshAppInput 实现 workerhandlers.AppInputRefresher。
//
// 流程:
//  1. 校验依赖(queries / uploader / cipher / resolveImage 任一缺失立即报错,
//     让 restart 失败比"静默用旧 input 重启"更安全);
//  2. 校验应用已绑定助手版本；
//  3. 加载版本并解析镜像 ref；
//  4. 取 organization / owner 上下文；
//  5. 解密 apps.newapi_key_ciphertext 拿到 sk- 明文(BuildAppInputData 需要);
//  6. 调 AssembleVersionInputData 装配版本数据（推 skill tar + 解析 routing）；
//  7. 调 workerhandlers.BuildAppInputData 装配 hermes.AppInputData;
//  8. 调 hermes.WriteAppInput 写到节点 apps/<id>/input/manifest.yaml +
//     resources/*.md（知识库不在 restart 路径重写，仍由 knowledge_sync 单独同步）；
//  9. 返回 AppInputRefreshResult（版本修订 + 镜像 ref），供 handler 记录 applied 信息。
//
// 任意步骤失败立即冒泡: handler 上层会把错误带回 worker 触发重试, 重试时
// 这里完全幂等(覆盖写 + DB 重新读最新值)。
func (r *appInputRefresher) RefreshAppInput(ctx context.Context, nodeID string, app sqlc.App) (workerhandlers.AppInputRefreshResult, error) {
	if r.queries == nil {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("appInputRefresher queries 未注入")
	}
	if r.uploader == nil {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("appInputRefresher uploader 未注入")
	}
	if r.cipher == nil {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("appInputRefresher cipher 未注入")
	}
	if nodeID == "" {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("appInputRefresher 收到空 nodeID")
	}
	if r.resolveImage == nil {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("appInputRefresher resolveImage 未注入")
	}
	if !app.VersionID.Valid {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("应用未绑定助手版本, 无法刷新 input")
	}
	version, err := r.queries.GetAssistantVersion(ctx, app.VersionID)
	if err != nil {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("加载助手版本失败: %w", err)
	}
	imageRef, ok := r.resolveImage(version.ImageID)
	if !ok {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("版本镜像 %s 未在配置中", version.ImageID)
	}
	org, err := r.queries.GetOrganization(ctx, app.OrgID)
	if err != nil {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("查询组织失败: %w", err)
	}
	owner, err := r.queries.GetUser(ctx, app.OwnerUserID)
	if err != nil {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("查询应用 owner 失败: %w", err)
	}
	// app.NewapiKeyCiphertext 为空意味着应用从未初始化完毕,
	// 此时调 restart 是误用; 让错误冒泡比写出无密钥的 manifest 更安全。
	if !app.NewapiKeyCiphertext.Valid || app.NewapiKeyCiphertext.String == "" {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("应用 newapi_key_ciphertext 为空, 无法刷新 input")
	}
	plain, err := r.cipher.Decrypt(app.NewapiKeyCiphertext.String)
	if err != nil {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("解密 api_key 失败: %w", err)
	}
	// 通过共用的 AssembleVersionInputData 装配版本数据（推 skill tar + 解析 routing），
	// 与 init 链路共享同一逻辑，避免两条链路写出的 manifest 版本字段漂移。
	versionData, err := workerhandlers.AssembleVersionInputData(ctx, version, app, nodeID, r.skillBlobs, r.uploader)
	if err != nil {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("装配版本输入数据失败: %w", err)
	}
	in := workerhandlers.BuildAppInputData(app, org, owner, string(plain), versionData, r.opts)
	// 复用 init handler 同款 adapter 把 (uploader + nodeID) 适配成 hermes.AppInputWriter,
	// 保证 init 与 restart 走完全一致的上传路径(底层都是 agent input/file 路由)。
	writer := workerhandlers.NewAppInputUploadAdapter(r.uploader, nodeID)
	if err := hermes.WriteAppInput(ctx, writer, in.AppID, in); err != nil {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("写入应用 input 失败: %w", err)
	}
	return workerhandlers.AppInputRefreshResult{
		VersionRevision: version.Revision,
		ImageRef:        imageRef,
	}, nil
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
