// 装配 manager 进程所需的所有跨包依赖。
// 这里只放"接口适配 + 多节点路由"等胶水逻辑；具体业务规则由 service 层负责。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	dockercli "github.com/docker/docker/client"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/agent"
	"oc-manager/internal/integrations/runtime"
	"oc-manager/internal/runtime/imagesync"
	"oc-manager/internal/service"
	"oc-manager/internal/store"
	"oc-manager/internal/store/sqlc"
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
	queries  nodeQueries
	tokens   *agent.TokenResolver
	httpAuth *http.Client
}

func newNodeClientResolver(queries nodeQueries, tokens *agent.TokenResolver) *nodeClientResolver {
	return &nodeClientResolver{
		queries: queries,
		tokens:  tokens,
		httpAuth: &http.Client{
			// 文件 API 与镜像 API 均按节点限速；30s 是合理上限，调用方自行 ctx 收紧。
			Timeout: 30 * time.Second,
		},
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
	client := agent.NewFileClient(node.AgentFileEndpoint.String, token)
	client.HTTPClient = n.httpAuth
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
		BaseURL:    node.AgentFileEndpoint.String,
		Token:      token,
		HTTPClient: n.httpAuth,
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
		BaseURL:    node.AgentFileEndpoint.String,
		Token:      token,
		HTTPClient: n.httpAuth,
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
		return sqlc.RuntimeNode{}, "", fmt.Errorf("节点 %s 的 agent token 未缓存（需要 rotate-bootstrap）: %w", nodeID, err)
	}
	return node, token, nil
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
	queries  knowledgeJobsQueries
	notifier service.JobNotifier
}

type knowledgeJobsQueries interface {
	ListRuntimeNodes(ctx context.Context, arg sqlc.ListRuntimeNodesParams) ([]sqlc.RuntimeNode, error)
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error)
}

func newKnowledgeSyncDispatcher(queries knowledgeJobsQueries, notifier service.JobNotifier) *knowledgeSyncDispatcher {
	return &knowledgeSyncDispatcher{queries: queries, notifier: notifier}
}

// DispatchOrgChange 给所有 active 节点入队一个 sync 任务。
func (d *knowledgeSyncDispatcher) DispatchOrgChange(ctx context.Context, orgID, relPath, changeType, masterPath string) error {
	nodes, err := d.queries.ListRuntimeNodes(ctx, sqlc.ListRuntimeNodesParams{Limit: 200, Offset: 0})
	if err != nil {
		return fmt.Errorf("查询节点失败: %w", err)
	}
	for _, node := range nodes {
		if node.Status != "active" {
			continue
		}
		if err := d.enqueue(ctx, knowledgeSyncJobInput{
			Scope:      "org",
			OrgID:      orgID,
			NodeID:     uuidToStringWiring(node.ID),
			ChangeType: changeType,
			RelPath:    relPath,
			MasterPath: masterPath,
		}); err != nil {
			return err
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
	Scope      string
	OrgID      string
	AppID      string
	NodeID     string
	ChangeType string
	RelPath    string
	MasterPath string
}

func (d *knowledgeSyncDispatcher) enqueue(ctx context.Context, input knowledgeSyncJobInput) error {
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
// 加密失败不冒泡：成功的 register 响应已经返回给 agent，持久化失败只走日志。
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
