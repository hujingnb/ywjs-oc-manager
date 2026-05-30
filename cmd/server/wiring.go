// 装配 manager 进程所需的所有跨包依赖。
// 这里只放"接口适配 + 多节点路由"等胶水逻辑；具体业务规则由 service 层负责。
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	dockercli "github.com/docker/docker/client"
	"github.com/google/uuid"
	null "github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/agent"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/integrations/ocops"
	"oc-manager/internal/integrations/runtime"
	"oc-manager/internal/service"
	"oc-manager/internal/store"
	"oc-manager/internal/store/sqlc"
	workerhandlers "oc-manager/internal/worker/handlers"
)

// nodeQueries 是 nodeClientResolver 需要的最小查询子集。
// 抽出接口便于测试用内存桩。
type nodeQueries interface {
	GetRuntimeNode(ctx context.Context, id string) (sqlc.RuntimeNode, error)
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
	// dockerCache / fileCache 按 nodeID 缓存并复用按节点构造的 client，避免每次操作新建
	// transport 导致空闲连接泄漏、临时端口耗尽（参见 agent.ClientCache 说明）。
	dockerCache *agent.ClientCache[*dockercli.Client]
	fileCache   *agent.ClientCache[*agent.AgentFileClient]
}

func newNodeClientResolver(queries nodeQueries, tokens *agent.TokenResolver) *nodeClientResolver {
	return &nodeClientResolver{
		queries: queries,
		tokens:  tokens,
		// docker client 被替换时调用 Close() 关闭其连接池。
		dockerCache: agent.NewClientCache(func(c *dockercli.Client) {
			if c != nil {
				_ = c.Close()
			}
		}),
		// file client 自身无 Close；替换时关闭其底层 http.Client 的空闲连接。
		fileCache: agent.NewClientCache(func(c *agent.AgentFileClient) {
			if c != nil && c.HTTPClient != nil {
				c.HTTPClient.CloseIdleConnections()
			}
		}),
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
	// 指纹覆盖 endpoint/token/CA：任一变更（节点 re-register、token 轮换、CA 重签）即重建并回收旧 client。
	fp := agent.Fingerprint(node.AgentFileEndpoint.String, token, node.AgentTlsCaCert.String)
	return n.fileCache.Get(nodeID, fp, func() (*agent.AgentFileClient, error) {
		httpClient, err := n.agentHTTPClient(node, 30*time.Second)
		if err != nil {
			return nil, err
		}
		client := agent.NewFileClient(node.AgentFileEndpoint.String, token)
		client.SetHTTPClient(httpClient)
		return client, nil
	})
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
	// 指纹覆盖 endpoint/token/CA：任一变更即重建并 Close 旧 docker client。
	fp := agent.Fingerprint(node.AgentDockerEndpoint.String, token, node.AgentTlsCaCert.String)
	return n.dockerCache.Get(nodeID, fp, func() (*dockercli.Client, error) {
		return agent.NewDockerClientForNode(node.AgentDockerEndpoint.String, token, node.AgentTlsCaCert.String)
	})
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
	// streamCache 独立于 inner.dockerCache：流式 client 不设 http.Client.Timeout，
	// transport 配置不同，必须分开缓存。
	streamCache *agent.ClientCache[*dockercli.Client]
}

func newStreamingDockerResolver(inner *nodeClientResolver) *streamingDockerResolver {
	return &streamingDockerResolver{
		inner: inner,
		streamCache: agent.NewClientCache(func(c *dockercli.Client) {
			if c != nil {
				_ = c.Close()
			}
		}),
	}
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
	// 指纹覆盖 endpoint/token/CA：任一变更即重建并 Close 旧流式 docker client。
	fp := agent.Fingerprint(node.AgentDockerEndpoint.String, token, node.AgentTlsCaCert.String)
	return s.streamCache.Get(nodeID, fp, func() (*dockercli.Client, error) {
		return agent.NewStreamingDockerClientForNode(node.AgentDockerEndpoint.String, token, node.AgentTlsCaCert.String)
	})
}

// lookupNode 同时返回节点行与 agent token；任何字段缺失立即报错让上层快速失败。
func (n *nodeClientResolver) lookupNode(ctx context.Context, nodeID string) (sqlc.RuntimeNode, string, error) {
	if nodeID == "" {
		return sqlc.RuntimeNode{}, "", fmt.Errorf("nodeID 不能为空")
	}
	node, err := n.queries.GetRuntimeNode(ctx, nodeID)
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
		return nil, fmt.Errorf("节点 %s 缺 agent_tls_ca_cert", node.ID)
	}
	pool, err := agent.BuildCertPool(node.AgentTlsCaCert.String)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    pool,
				MinVersion: tls.VersionTLS12,
			},
			IdleConnTimeout:     agent.IdleConnTimeout,
			MaxIdleConnsPerHost: agent.MaxIdleConnsPerHost,
		},
	}, nil
}

// ocopsEndpointResolver 把 service.OcOpsResolver 适配为 workerhandlers.ChannelEndpointResolver：
// 仅取出 OcOpsAppLocation.Endpoint，让 channel_start_login worker 不直接依赖 service 类型。
type ocopsEndpointResolver struct {
	resolver service.OcOpsResolver
}

// ResolveEndpoint 解析 appID 对应的 oc-ops 调用坐标。
func (r ocopsEndpointResolver) ResolveEndpoint(ctx context.Context, appID string) (ocops.Endpoint, error) {
	loc, err := r.resolver.Resolve(ctx, appID)
	if err != nil {
		return ocops.Endpoint{}, err
	}
	return loc.Endpoint, nil
}

// ocopsBindingLocationResolver 把 service.OcOpsResolver 适配为 channel.OcOpsLocationResolver：
// 仅取出 OcOpsAppLocation.Endpoint 和 Supported，隔离 channel 包对 service 包的直接依赖
// （channel→service 会形成循环依赖，因此在 main 包做适配）。
type ocopsBindingLocationResolver struct {
	inner service.OcOpsResolver
}

// Resolve 实现 channel.OcOpsLocationResolver，把 OcOpsAppLocation 拆成 Endpoint+Supported 返回。
func (a ocopsBindingLocationResolver) Resolve(ctx context.Context, appID string) (ocops.Endpoint, bool, error) {
	loc, err := a.inner.Resolve(ctx, appID)
	if err != nil {
		return ocops.Endpoint{}, false, err
	}
	return loc.Endpoint, loc.Supported, nil
}

// appInputRefresherQueries 是 appInputRefresher 需要的最小 DB 查询子集。
// 抽接口便于单测注入内存桩, 不必引入完整 *sqlc.Queries 依赖。
// k8s 下 pod 配置由 bootstrap 在启动时交付，restart 不再向节点写 manifest，
// 因此只保留 GetAssistantVersion 供版本镜像解析使用。
type appInputRefresherQueries interface {
	GetAssistantVersion(ctx context.Context, id string) (sqlc.AssistantVersion, error)
}

// appInputRefresher 实现 workerhandlers.AppInputRefresher：k8s 下 pod 配置由 bootstrap 在
// 启动时交付，restart 不再向节点写 manifest；这里只解析「当前绑定版本的镜像 ref 与 revision」，
// 供 restart handler 做镜像变更检测与记录 applied 版本。
type appInputRefresher struct {
	// queries 用于 GetAssistantVersion，取绑定版本的 image_id 与 revision。
	queries appInputRefresherQueries
	// resolveImage 把版本 image_id 解析为完整 imageRef（含 tag）。
	// nil 时 RefreshAppInput 直接报错，无法确定运行时镜像 ref。
	resolveImage func(imageID string) (string, bool)
}

// newAppInputRefresher 构造生产装配用的 refresher。
// 只依赖 queries 与 resolveImage 两个依赖，其余 uploader/cipher/skillBlobs/opts 已全部移除。
func newAppInputRefresher(queries appInputRefresherQueries, resolveImage func(string) (string, bool)) *appInputRefresher {
	return &appInputRefresher{queries: queries, resolveImage: resolveImage}
}

// RefreshAppInput 只解析当前绑定版本的镜像 ref 与 revision（不再写节点 manifest）。
// nodeID 参数保留以匹配 workerhandlers.AppInputRefresher 接口，但 k8s 下忽略。
func (r *appInputRefresher) RefreshAppInput(ctx context.Context, _ string, app sqlc.App) (workerhandlers.AppInputRefreshResult, error) {
	if r.queries == nil || r.resolveImage == nil {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("appInputRefresher 依赖未注入")
	}
	if !app.VersionID.Valid {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("应用未绑定助手版本, 无法解析镜像")
	}
	version, err := r.queries.GetAssistantVersion(ctx, app.VersionID.String)
	if err != nil {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("加载助手版本失败: %w", err)
	}
	imageRef, ok := r.resolveImage(version.ImageID)
	if !ok {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("版本镜像 %s 未在配置中", version.ImageID)
	}
	return workerhandlers.AppInputRefreshResult{VersionRevision: version.Revision, ImageRef: imageRef}, nil
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

// jsonMarshal 是 cmd/server 内部 json.Marshal 的简短封装，便于 dispatcher 复用。
var jsonMarshal = json.Marshal

// runtimeRefreshJobsQueries 是 runtimeRefreshDispatcher 用到的 sqlc 子集。
// spec-A2b：ListRunningApps 返回 []string（只含 app id），不再含节点/容器字段。
type runtimeRefreshJobsQueries interface {
	ListRunningApps(ctx context.Context) ([]string, error)
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) error
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
// spec-A2b：ListRunningApps 返回 []string，直接遍历 app id 字符串。
func enqueuePerRunningApp(ctx context.Context, queries runtimeRefreshJobsQueries, notifier service.JobNotifier, jobType string, priority int32, maxAttempts int32) error {
	appIDs, err := queries.ListRunningApps(ctx)
	if err != nil {
		return fmt.Errorf("列出 running 应用失败: %w", err)
	}
	for _, appID := range appIDs {
		payload, err := jsonMarshal(map[string]any{"app_id": appID})
		if err != nil {
			continue
		}
		newJobID := uuid.NewString()
		if err := queries.CreateJob(ctx, sqlc.CreateJobParams{
			ID:          newJobID,
			Type:        jobType,
			Priority:    priority,
			MaxAttempts: maxAttempts,
			RunAfter:    time.Now(),
			PayloadJson: payload,
		}); err != nil {
			continue
		}
		if notifier != nil {
			_ = notifier.Enqueue(ctx, newJobID)
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
	ciphertext, err := l.store.Get(ctx, nodeID)
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
	ciphertext, err := c.Encrypt([]byte(token))
	if err != nil {
		return fmt.Errorf("加密 agent token 失败: %w", err)
	}
	// AgentTokenStore.Set 现在接收 string nodeID（已转换完毕）。
	return s.Set(ctx, nodeID, ciphertext)
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
	// orgID 标识当前刷新器绑定的组织（string UUID）。
	orgID string
	// username/password 是组织在 new-api 中的登录凭据，只保留在内存中用于刷新 access_token。
	username string
	password string
}

// RefreshAccessToken 刷新组织在 new-api 中的 access_token 并写回密文凭据。
func (r *orgCredentialsRefresher) RefreshAccessToken(ctx context.Context) (string, error) {
	// GetOrganizationForUpdate 现在接收 string。
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
	if err := r.store.UpdateOrganizationCredentialsCiphertext(ctx, sqlc.UpdateOrganizationCredentialsCiphertextParams{
		ID:                              org.ID,
		NewapiUserCredentialsCiphertext: null.StringFrom(ciphertext),
	}); err != nil {
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
	// app.OrgID 现在是 string，直接传入。
	org, err := f.store.GetOrganization(ctx, app.OrgID)
	if err != nil {
		return nil, fmt.Errorf("查询组织失败: %w", err)
	}
	creds, err := service.DecryptOrganizationCredentials(org, f.cipher)
	if err != nil {
		return nil, err
	}
	if !org.NewapiUserID.Valid || org.NewapiUserID.String == "" {
		return nil, fmt.Errorf("组织 %s 未持有 new-api 用户 id", org.ID)
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
