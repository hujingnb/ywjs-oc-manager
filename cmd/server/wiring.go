// 装配 manager 进程所需的所有跨包依赖。
// 这里只放"接口适配 + 多节点路由"等胶水逻辑；具体业务规则由 service 层负责。
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	dockercli "github.com/docker/docker/client"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/agent"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/integrations/runtime"
	"oc-manager/internal/runtime/imagesync"
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
//   - imagesync.AgentImageClient（InspectImage/LoadImage）
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
	httpClient, err := n.fileHTTPClient(node)
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

// InspectImage 适配 imagesync.AgentImageClient 接口。
func (n *nodeClientResolver) InspectImage(ctx context.Context, nodeID, image string) (imagesync.RemoteImageInfo, error) {
	node, token, err := n.lookupNode(ctx, nodeID)
	if err != nil {
		return imagesync.RemoteImageInfo{}, err
	}
	inner := imagesync.AgentHTTPClient{
		BaseURL: node.AgentFileEndpoint.String,
		Token:   token,
	}
	inner.HTTPClient, err = n.fileHTTPClient(node)
	if err != nil {
		return imagesync.RemoteImageInfo{}, err
	}
	return inner.InspectImage(ctx, nodeID, image)
}

// LoadImage 适配 imagesync.AgentImageClient 接口。
func (n *nodeClientResolver) LoadImage(ctx context.Context, nodeID, image string, archive io.Reader) (imagesync.RemoteImageInfo, error) {
	node, token, err := n.lookupNode(ctx, nodeID)
	if err != nil {
		return imagesync.RemoteImageInfo{}, err
	}
	inner := imagesync.AgentHTTPClient{
		BaseURL: node.AgentFileEndpoint.String,
		Token:   token,
	}
	inner.HTTPClient, err = n.fileHTTPClient(node)
	if err != nil {
		return imagesync.RemoteImageInfo{}, err
	}
	return inner.LoadImage(ctx, nodeID, image, archive)
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

func (n *nodeClientResolver) fileHTTPClient(node sqlc.RuntimeNode) (*http.Client, error) {
	if !node.AgentTlsCaCert.Valid || strings.TrimSpace(node.AgentTlsCaCert.String) == "" {
		return nil, fmt.Errorf("节点 %s 缺 agent_tls_ca_cert", uuidToStringWiring(node.ID))
	}
	pool, err := agent.BuildCertPool(node.AgentTlsCaCert.String)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		// 文件 API 与镜像 API 均按节点限速；30s 是合理上限，调用方自行 ctx 收紧。
		Timeout: 30 * time.Second,
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
// 任意节点查询失败/job 写入失败立即冒泡，由 service 决定是否中断主流程；
// 当前实现把 dispatcher 错误吞在 service 层（参见 KnowledgeService 的 _ =），
// 因为主副本已经写入，不应因为同步失败回滚。
type knowledgeSyncDispatcher struct {
	queries    knowledgeJobsQueries
	notifier   service.JobNotifier
	syncStatus knowledgeSyncStatusMarker
}

type knowledgeJobsQueries interface {
	ListRuntimeNodes(ctx context.Context, arg sqlc.ListRuntimeNodesParams) ([]sqlc.RuntimeNode, error)
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error)
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

// RetryOrgNode 触发指定 (org, node) 重新同步：入队一个 'noop' change_type 的 sync job，
// worker 处理时只走 status writer 把状态推到 synced（无文件改动 → upload/delete 都跳过）。
//
// 当前实现简化为入队 'noop' job：worker handler 看到 change_type='noop' 直接 mark synced。
// 这避免引入"知识库目录全量打 tar 推过去"的额外路径，保持单 job 类型。
func (d *knowledgeSyncDispatcher) RetryOrgNode(ctx context.Context, orgID, nodeID string) error {
	if err := d.enqueue(ctx, knowledgeSyncJobInput{
		Scope:      "org",
		OrgID:      orgID,
		NodeID:     nodeID,
		ChangeType: "noop",
		RelPath:    "(retry)", // 占位，noop 不读
		MasterPath: "(retry)",
	}); err != nil {
		return err
	}
	if d.syncStatus != nil {
		_ = d.syncStatus.MarkOrgNodePending(ctx, orgID, nodeID)
	}
	return nil
}

// DispatchOrgChange 给所有 active 节点入队一个 sync 任务。
// 入队成功后立刻写 (org, node) = pending 状态，让前端立即可见"同步中"。
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
	return nil
}

// DispatchAppChange 给应用所在节点入队 sync 任务。
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
	return d.enqueue(ctx, knowledgeSyncJobInput{
		Scope:      "app",
		OrgID:      orgID,
		AppID:      appID,
		NodeID:     uuidToStringWiring(app.RuntimeNodeID),
		ChangeType: changeType,
		RelPath:    relPath,
		MasterPath: masterPath,
	})
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
	// MasterPath 是 manager 主副本中的文件路径，worker 仅通过 agent API 读取同步内容。
	MasterPath string
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

// imageDistributorWrapper 把 service.ImageDistributionService 适配成 handlers.ImageDistributor 的
// (any, error) 自由签名。Go 接口要求 exact 返回类型匹配，所以转一层。
type imageDistributorWrapper struct {
	svc *service.ImageDistributionService
}

func newImageDistributorWrapper(svc *service.ImageDistributionService) *imageDistributorWrapper {
	return &imageDistributorWrapper{svc: svc}
}

// EnsureRuntimeImage 把 service 的具体结构体返回值转成 handlers.ImageDistributor 期望的 any。
func (w *imageDistributorWrapper) EnsureRuntimeImage(ctx context.Context, nodeID, image string) (any, error) {
	return w.svc.EnsureRuntimeImage(ctx, nodeID, image)
}

// appDirInitializerAdapter 把 *runtime.AgentBackedAdapter 适配成
// handlers.AgentDirInitializer，仅暴露 InitAppDirs 一个方法，避免 handler 依赖
// 整个 adapter 类型导致测试 mock 复杂。生产装配传 runtimeAdapter 即可。
type appDirInitializerAdapter struct {
	adapter interface {
		InitAppDirs(ctx context.Context, nodeID, appID string) error
	}
}

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
