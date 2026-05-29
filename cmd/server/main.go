// Package main 是 manager-api HTTP 服务入口。
//
// @title           Agent Runtime Manager API
// @version         1.0
// @description     基于 Hermes Agent runtime 的多企业管理后台 API。
// @BasePath        /api/v1
//
// @securityDefinitions.apikey BearerAuth
// @in              header
// @name            Authorization
// @description     登录后获得的 JWT access token，前缀 "Bearer "。
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"

	"oc-manager/internal/api"
	"oc-manager/internal/api/middleware"
	"oc-manager/internal/audit"
	"oc-manager/internal/auth"
	"oc-manager/internal/config"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/agent"
	"oc-manager/internal/integrations/channel"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/integrations/ocops"
	"oc-manager/internal/integrations/ragflow"
	"oc-manager/internal/integrations/runtime"
	"oc-manager/internal/integrations/storage"
	managerlog "oc-manager/internal/log"
	"oc-manager/internal/migrations"
	"oc-manager/internal/redis"
	"oc-manager/internal/runtime/imagecoord"
	"oc-manager/internal/scheduler"
	"oc-manager/internal/service"
	"oc-manager/internal/store"
	"oc-manager/internal/worker"
	"oc-manager/internal/worker/handlers"
	"oc-manager/internal/worker/reaper"

	migrate "github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// ocopsBaseURLTemplate 是 oc-ops 服务基址的约定占位模板，含一个 %s 占位符（以 appID 替换）。
//
// TODO(spec-A): spec-E 不做真实 k8s 寻址，此模板仅为 service.OcOpsResolverFromStore 提供
// 可拼装的占位基址；spec-A 将由 client-go 解析的真实 Service DNS 取代，并注入 per-app
// OC_OPS_TOKEN（当前 Endpoint.Token 留空）。
const ocopsBaseURLTemplate = "http://app-%s-ocops.oc-apps.svc:8080"

func main() {
	configPath := os.Getenv("OCM_CONFIG")
	if configPath == "" {
		configPath = "config/manager.yaml"
	}

	cfg, err := config.LoadFile(configPath)
	if err != nil {
		stdlog.Fatalf("加载配置失败: %v", err)
	}
	if err := runManager(context.Background(), cfg, os.Stderr); err != nil {
		stdlog.Fatalf("manager 退出: %v", err)
	}
}

// runManager 是 main 的可测试入口。
//
// 阶段：
//  1. 校验 master_key 并构造 Cipher（fail-fast）；
//  2. 打开 PostgreSQL pool 与 Redis Queue；
//  3. 装配 service、worker handler、worker pool、scheduler loop、HTTP server；
//  4. 用 errgroup 并发跑 server / pool / loop；ctx 取消或收到 SIGINT/SIGTERM 时优雅退出。
//
// 错误以 fmt.Errorf 形式冒泡到调用方，便于 main 用 log.Fatalf 输出，也便于测试用 ctx 取消触发干净退出。
func runManager(ctx context.Context, cfg config.Config, logOut io.Writer) error {
	// 构造结构化 logger：RedactingWriter 已在 NewSlogLogger 内部包装，
	// 所有日志输出自动脱敏（密码 / token / sk- key）。
	// 顺序要求：先 NewSlogLogger，再 SetRequestIDExtractor，再 SetDefault，
	// 确保首批日志也能从 ctx 中提取 trace_id。
	logger := managerlog.NewSlogLogger(logOut)
	managerlog.SetRequestIDExtractor(middleware.RequestIDFromContext)
	slog.SetDefault(logger)

	masterKey, err := base64.StdEncoding.DecodeString(cfg.Security.MasterKey)
	if err != nil {
		return fmt.Errorf("master_key base64 解码失败: %w", err)
	}
	// master_key 是所有落库密文的根密钥；长度校验交给 auth.NewCipher，失败则禁止继续启动。
	cipher, err := auth.NewCipher(masterKey)
	if err != nil {
		return fmt.Errorf("初始化 cipher 失败: %w", err)
	}

	dbStore, err := store.Open(ctx, cfg.Database.URL)
	if err != nil {
		return fmt.Errorf("连接数据库失败: %w", err)
	}
	defer dbStore.Close()

	// 启动时自动执行 schema migrate up。
	// 与 cmd/migrate 共用 internal/migrations 的 go:embed FS,逻辑保持一致;
	// golang-migrate 内置 PG advisory lock,多副本同时启动也只有一个真正跑 migration,
	// 其它实例会等待 lock 释放后跳过 ErrNoChange。
	// 失败 fail-fast,避免新 schema 字段缺失导致 sqlc 查询 panic。
	if err := autoMigrate(cfg.Database.URL, logger); err != nil {
		return fmt.Errorf("执行启动迁移失败: %w", err)
	}

	redisQueue := redis.NewRedisQueue(redis.Config{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
		QueueKey: cfg.Redis.KeyPrefix + "jobs:queue",
	})
	defer redisQueue.Close()

	tokenManager, err := auth.NewTokenManager(
		cfg.Auth.JWTAccessSecret,
		cfg.Auth.JWTRefreshSecret,
		cfg.Auth.AccessTokenTTL.Duration,
		cfg.Auth.RefreshTokenTTL.Duration,
	)
	if err != nil {
		return fmt.Errorf("初始化认证令牌管理器失败: %w", err)
	}

	authService := service.NewAuthService(dbStore.Queries, tokenManager)
	memberService := service.NewMemberService(dbStore.Queries, hashPasswordWithDefault)
	// nodeSelector 复用 sqlc 生成的 ListActiveNodesWithAppCounts，给 OnboardingService 自动选节点用。
	nodeSelector := service.NewSQLNodeSelector(dbStore.Queries)
	onboardingService := service.NewMemberOnboardingService(store.NewOnboardingRunner(dbStore), hashPasswordWithDefault, nodeSelector)
	auditService := service.NewAuditService(dbStore.Queries)
	runtimeNodeStore := store.NewRuntimeNodeStore(dbStore)
	runtimeNodeService := service.NewRuntimeNodeService(runtimeNodeStore, hashTokenSHA256)

	var ragflowClient service.RAGFlowKnowledgeClient
	if cfg.RAGFlow.BaseURL != "" || cfg.RAGFlow.APIKey != "" {
		ragflowHTTPClient, err := ragflow.NewClient(cfg.RAGFlow.BaseURL, cfg.RAGFlow.APIKey, cfg.RAGFlow.RequestTimeout.Duration)
		if err != nil {
			return fmt.Errorf("初始化 RAGFlow 客户端失败: %w", err)
		}
		ragflowClient = ragflowHTTPClient
	}
	knowledgeService := service.NewKnowledgeService(dbStore.Queries, ragflowClient)
	knowledgeService.SetDatasetChunkMethod(cfg.RAGFlow.ChunkMethod)
	onboardingService.SetKnowledgeDatasetProvisioner(knowledgeService)
	appService := service.NewAppService(dbStore.Queries)
	appService.SetJobNotifier(redisQueue)
	// 注入版本镜像解析器：AppService 计算 version_synced 时需要把版本 image_id 解析成镜像 ref。
	appService.SetImageResolver(runtimeImageAdapter{images: cfg.Hermes.RuntimeImages})
	runtimeOpService := service.NewRuntimeOperationService(dbStore.Queries, logger, redisQueue)
	resourceMetricsService := service.NewResourceMetricsService(dbStore.Queries)
	// usage / organization service 在装配 newapi client 之后再实例化（见下方）；
	// 这里仅声明变量，真实赋值发生在 newapi wiring 段。
	var usageService *service.UsageService
	var organizationService *service.OrganizationService
	var rechargeService *service.RechargeService
	var modelCatalogService *service.ModelCatalogService
	// runtime inspector 在 runtimeAdapter 构造之后注入；这里先声明字段，后面赋值。

	agentTokenResolver := agent.NewTokenResolver()
	agentTokenStore := store.NewAgentTokenStore(dbStore)
	agentTokenResolver.SetPersistentLoader(newPersistentTokenLoader(agentTokenStore, cipher))
	agentTokenSink := func(nodeID, token string) {
		agentTokenResolver.Set(nodeID, token)
		// 加密入库；任何错误只走日志，不阻断 enroll 响应。
		if err := persistAgentToken(context.Background(), agentTokenStore, cipher, nodeID, token); err != nil {
			logger.Error("持久化 agent token 失败", "node_id", nodeID, "error", err)
		}
	}
	nodeResolver := newNodeClientResolver(dbStore.Queries, agentTokenResolver)

	// imagecoord.Coordinator 跨 manager 实例对 pull / sync 做 single-flight
	// 并通过 Redis Pub/Sub 广播进度。这里单独开一个 go-redis client 给 DistLocker /
	// ProgressBus 使用,与 redisQueue 共享同一 Redis 物理实例但连接池分离,
	// 避免长时间 Subscribe 占用 queue 用到的连接。
	imagecoordRedis := goredis.NewClient(&goredis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	defer imagecoordRedis.Close()
	distLocker := redis.NewRedisDistLocker(imagecoordRedis)
	progressBus := redis.NewRedisProgressBus(imagecoordRedis)
	// Coordinator 按 (nodeID, imageRef) 粒度加锁，让 agent 直接从公网 registry 拉取镜像。
	imageCoord := imagecoord.NewCoordinator(distLocker, progressBus, uuid.NewString())
	runtimeAdapter := runtime.NewAgentBackedAdapter(nodeResolver, nodeResolver)
	// streamingResolver 构造一次复用：runtimeAdapter.DockerClientForNode（镜像拉取流式 NDJSON）
	// 和 channel 微信扫码 ExecAttach 均属于长连接场景，都必须用无 timeout 的 docker client。
	// nodeClientResolver.DockerClient 带 30s Timeout，长连接会在 30s 后被强制掐断，
	// newStreamingDockerResolver 使用 agent.NewStreamingDockerClientForNode 不设 Timeout。
	streamingResolver := newStreamingDockerResolver(nodeResolver)
	runtimeAdapter.SetStreamingDocker(streamingResolver)
	runtimeOpService.SetInspector(newRuntimeInspectorWrapper(runtimeAdapter))
	workspaceService := service.NewWorkspaceService(dbStore.Queries, runtimeAdapter, cfg.App.DataRoot)

	// oc-ops HTTP 客户端 + app 坐标解析器：cron / kanban / 微信扫码登录均改走
	// oc-ops 类型化 REST / SSE，不再经 runtimeAdapter docker exec。
	// 30s 超时仅约束普通 RPC（DoJSON）；SSE 长连接（kanban watch / 微信扫码，
	// pod 侧 qr_login 超时达 480s）由 ocops.Client 内部的无 Timeout streamHTTP 执行、
	// 生命周期靠调用方 ctx 控制——http.Client.Timeout 会一并中断 Body 读取，不能用于
	// 流式订阅（与下方 streamingResolver 对 docker ExecAttach 的处理同理）。
	ocopsClient := ocops.NewClient(&http.Client{Timeout: 30 * time.Second})
	// ocopsResolver 把 appID 解析为 oc-ops 调用坐标。
	// TODO(spec-A): ocopsBaseURLTemplate 当前为约定占位模板，spec-E 不做真实 k8s 寻址；
	// spec-A 将替换为 client-go 解析的真实 Service DNS，并注入 per-app OC_OPS_TOKEN。
	ocopsResolver := service.NewOcOpsResolverFromStore(dbStore.Queries, ocopsBaseURLTemplate)

	channelRegistry := channel.NewRegistry()
	channelService := service.NewChannelService(dbStore.Queries, channelRegistry, redisQueue)
	// channel 微信扫码 ExecAttach 是长连接(等用户扫码可达数分钟),
	// 必须用 streamingDockerResolver 拿无 timeout 的 docker client,
	// 否则 http.Client.Timeout=30s 会强制关闭 hijack 后的底层连接,
	// 导致 stream EOF + JSON 解析失败 + 容器内 oc-weixin-login.py 进程 orphan。
	wechatExecutor := channel.NewDockerExecutor(streamingResolver)
	// 微信扫码登录走 oc-ops HTTP SSE：runner 持 ocopsClient（满足 hermes.ChannelLoginStreamer），
	// 每次登录按 AuthInput.Endpoint（worker 经 ocopsResolver 解析后注入）路由到目标实例。
	wechatRunner := channel.NewDockerCommandRunner(ocopsClient)
	// DockerBindingResolver 仍走 docker exec 读容器内 plugin state（spec-A 再改造），
	// 因此 wechatExecutor / streamingResolver 装配保留。
	wechatResolver := channel.NewDockerBindingResolver(wechatExecutor)
	if err := channelRegistry.Register(channel.NewWeChatAdapter(wechatRunner)); err != nil {
		return fmt.Errorf("注册微信渠道失败: %w", err)
	}

	// new-api 装配：
	//   - newapiClient 是顶层 admin 视角；可调创建 user / 充值 / 查日志 / 查 quota；
	//   - newapiFactory 在 worker handler 跑 job 时把 (app→org→credentials) 翻译成
	//     user-scoped client，用于创 token / 拿完整 sk- / 改 token 状态；
	//   - cfg.NewAPI.BaseURL 为空时所有以上能力降级为不可用，handler 调用直接报错。
	var newapiClient *newapi.Client
	var newapiFactory handlers.NewAPIClientFactory
	// appInitAuditHelper 在 newapi 配置完成时由内层赋值；若 newapi 未启用，AppInitializeConfig
	// 拿到 nil 跳过审计写入，行为与 OOS-3 helper 自身的 nil 安全约定一致。
	var appInitAuditHelper *audit.NewAPIAuditHelper
	if cfg.NewAPI.BaseURL != "" {
		newapiClient = newapi.NewClient(cfg.NewAPI.BaseURL, cfg.NewAPI.AdminToken, cfg.NewAPI.AdminUserID)
		newapiClient.SetModelRelayToken(cfg.NewAPI.ModelRelayToken)
		newapiFactory = &orgScopedClientFactory{
			client: newapiClient,
			store:  dbStore.Queries,
			cipher: cipher,
		}
		rechargeService = service.NewRechargeService(dbStore.Queries, newapiClient)
		// newapiAuditHelper 实现 service.NewAPIFailureAuditor，供各 service / worker 写失败审计。
		newapiAuditHelper := audit.NewNewAPIAuditHelper(auditService)
		usageService = service.NewUsageService(dbStore.Queries, newapiClient, newapiAuditHelper)
		organizationService = service.NewOrganizationService(dbStore.Queries, newapiClient, cipher, newapiAuditHelper)
		modelCatalogService = service.NewModelCatalogService(newapiClient)
		// 同时把 helper 暴露到 if 块外，给 AppInitializeConfig.AuditHelper 装配使用。
		appInitAuditHelper = newapiAuditHelper
	} else {
		// 未配 newapi：仍构造一个会在调用时 fail-soft 的 service（store/client 全 nil），
		// 保持 cmd/server 装配路径稳定，调用时返回 ErrUsageUnavailable / 创建组织报错。
		usageService = service.NewUsageService(nil, nil, nil)
		organizationService = service.NewOrganizationService(dbStore.Queries, nil, nil, nil)
	}
	// 按配置选择 skill 主副本存储实现；两种实现都同时满足 SkillBlobStore 与 worker 的 SkillBlobReader。
	// S3 启用时走对象存储（需 MinIO / 云 OSS），否则退回本地 FS（无 MinIO 的最小开发仍可用）。
	var skillBlobStore interface {
		PutSkill(versionID, skillName string, data []byte) (string, error)
		DeleteSkill(relPath string) error
		OpenSkill(relPath string) (io.ReadCloser, error)
	}
	if cfg.Storage.S3.Enabled {
		s3cfg := storage.S3Config{
			Endpoint:        cfg.Storage.S3.Endpoint,
			Region:          cfg.Storage.S3.Region,
			Bucket:          cfg.Storage.S3.Bucket,
			AccessKeyID:     cfg.Storage.S3.AccessKeyID,
			SecretAccessKey: cfg.Storage.S3.SecretAccessKey,
			UsePathStyle:    cfg.Storage.S3.UsePathStyle,
			STSRoleARN:      cfg.Storage.S3.STSRoleARN,
		}
		objStore := storage.NewS3ObjectStore(s3cfg)
		skillBlobStore = service.NewS3SkillBlobStore(objStore, cfg.Storage.S3.PresignTTL.Duration)
	} else {
		skillBlobStore = service.NewFSSkillBlobStore(cfg.App.DataRoot)
	}

	// 助手版本 service：镜像来自配置、模型校验走 new-api 目录、skill tar 存数据根目录。
	// modelCatalogService 为 nil 时（未配 newapi）跳过构造，路由自动不注册。
	var assistantVersionService *service.AssistantVersionService
	if modelCatalogService != nil {
		assistantVersionService = service.NewAssistantVersionService(
			store.NewAssistantVersionStore(dbStore),
			runtimeImageAdapter{images: cfg.Hermes.RuntimeImages},
			modelValidatorAdapter{catalog: modelCatalogService},
			skillBlobStore,
			0,
		)
		// 助手版本服务作为组织 allowlist 校验器：组织创建/编辑时校验所选版本 id 都存在。
		organizationService.SetVersionValidator(assistantVersionService)
	}
	organizationService.SetKnowledgeDatasetProvisioner(knowledgeService)
	platformOverviewService := service.NewPlatformOverviewService(dbStore.Queries)

	registry := handlers.NewRegistry()
	// runtimeAdapter 同时实现 AgentDirInitializer / ContainerCreator / ContainerLifecycle
	// 三个接口（前者经 InitAppDirs 调用 agent file API，后两者经 docker proxy）。
	appInitHandler := handlers.NewAppInitializeHandler(
		dbStore.Queries,
		appDirInitializerAdapter{adapter: runtimeAdapter},
		runtimeAdapter,
		runtimeAdapter,
		newapiFactory,
		handlers.AppInitializeConfig{
			SystemPromptTemplate: cfg.Hermes.SystemPromptTemplate,
			PlatformPrompt:       cfg.Hermes.SystemPromptTemplate,
			Cipher:               cipher,
			// DataDir 字段保留供其他特定场景使用；Hermes 文件分发已走 UploadAppInputFile
			// (apps/<id>/input/)，不再在 manager 本机 DataDir 下写入配置文件。
			DataDir:           cfg.App.DataRoot,
			NewAPIBaseURL:     cfg.NewAPI.BaseURL,
			ContainerNetworks: cfg.Hermes.ContainerNetworks,
			LLM: handlers.AppInitializeLLMConfig{
				BaseURL:         cfg.Hermes.LLM.BaseURL,
				DefaultProvider: cfg.Hermes.LLM.DefaultProvider,
				DefaultModel:    cfg.Hermes.LLM.DefaultModel,
			},
			AuditHelper: appInitAuditHelper,
			ResolveRuntimeImage: func(imageID string) (string, bool) {
				return config.ResolveRuntimeImage(cfg.Hermes.RuntimeImages, imageID)
			},
			SkillBlobs:            skillBlobStore,
			ManagerRuntimeBaseURL: cfg.Hermes.ManagerRuntimeBaseURL,
		},
	)
	// runtimeAdapter 同时实现 AppInputUploader (UploadAppInputFile)，在多节点部署
	// 下把 manifest.yaml + resources/*.md 上传到目标节点 agent 的
	// dataRoot/apps/<id>/input/，容器内 oc-entrypoint 在启动时翻译成 hermes 自有 schema。
	appInitHandler.SetAppInputUploader(runtimeAdapter)
	// 注入镜像拉取协调器：phasePullRuntimeImage 通过 imageCoord 在目标 agent 节点直接
	// pull hermes runtime 镜像，Redis 锁 + Pub/Sub 保证集群内 single-flight + 跨实例进度广播。
	appInitHandler.SetImagePullCoord(imageCoord)
	// 注入 per-node Docker 客户端工厂：runtimeAdapter 已实现 DockerClientForNode，
	// 返回指向目标节点 agent docker proxy 的 Docker SDK client。
	appInitHandler.SetNodeDockerProvider(runtimeAdapter)
	if err := registry.Register("app_initialize", appInitHandler.Handle); err != nil {
		return fmt.Errorf("注册 app_initialize handler 失败: %w", err)
	}
	if err := registry.Register("app_start_container", handlers.NewAppStartContainerHandler(dbStore.Queries, runtimeAdapter).Handle); err != nil {
		return fmt.Errorf("注册 app_start_container handler 失败: %w", err)
	}
	if err := registry.Register("app_stop_container", handlers.NewAppStopContainerHandler(dbStore.Queries, runtimeAdapter).Handle); err != nil {
		return fmt.Errorf("注册 app_stop_container handler 失败: %w", err)
	}
	restartHandler := handlers.NewAppRestartContainerHandler(dbStore.Queries, runtimeAdapter)
	// 注入 session cleaner:restart 在容器实际重启前清 .hermes/sessions/,
	// 让 Hermes 启动新 session 时 snapshot 最新 SOUL.md(含改后的 model / persona /
	// 知识库等)。覆盖所有触发 restart 的入口(改 model / 重启 / persona 更新 / 未来其他)。
	restartHandler.SetSessionCleaner(runtimeAdapter)
	// 注入 input refresher: restart 在容器 stop 之前先把节点上的 apps/<id>/input/
	// manifest.yaml + resources/*.md 重写成 DB 当前快照。镜像 oc-entrypoint 在容器
	// 启动时根据该目录幂等重渲染 config.yaml / SOUL.md / skills, 所以只要 input
	// 文件是新的, restart 后容器内 hermes 自然加载到改后的 model / 三层 prompt / persona。
	// 字段取值复用 BuildAppInputData, 与 AppInitializeHandler.writeAppInput 严格等价。
	restartHandler.SetInputRefresher(newAppInputRefresher(
		dbStore.Queries,
		runtimeAdapter,
		cipher,
		func(imageID string) (string, bool) {
			return config.ResolveRuntimeImage(cfg.Hermes.RuntimeImages, imageID)
		},
		skillBlobStore,
		handlers.AppInputBuildOptions{
			PlatformPrompt:        cfg.Hermes.SystemPromptTemplate,
			NewAPIBaseURL:         cfg.NewAPI.BaseURL,
			DefaultModel:          cfg.Hermes.LLM.DefaultModel,
			ManagerRuntimeBaseURL: cfg.Hermes.ManagerRuntimeBaseURL,
		},
	))
	// 注入 job notifier：restart 检测到镜像变更时入队 app_initialize job 后即时唤醒 worker。
	restartHandler.SetJobNotifier(redisQueue)
	if err := registry.Register("app_restart_container", restartHandler.Handle); err != nil {
		return fmt.Errorf("注册 app_restart_container handler 失败: %w", err)
	}
	if err := registry.Register("app_delete", handlers.NewAppDeleteHandler(dbStore.Queries, runtimeAdapter, newapiFactory, nil, knowledgeService).Handle); err != nil {
		return fmt.Errorf("注册 app_delete handler 失败: %w", err)
	}
	if err := registry.Register(domain.JobTypeChannelStartLogin, handlers.NewChannelStartLoginHandler(dbStore.Queries, channelRegistry, ocopsEndpointResolver{resolver: ocopsResolver}).Handle); err != nil {
		return fmt.Errorf("注册 channel_start_login handler 失败: %w", err)
	}
	channelCheckHandler := handlers.NewChannelCheckBindingHandler(dbStore.Queries, channelRegistry, wechatResolver)
	// Hermes 时代凭证由容器内 oc-channel-login 自管,manager 不再渲染 .env;
	// bound 后仅触发容器重启让 hermes 重新读 platforms 配置,因此只注入 restarter。
	channelCheckHandler.SetRestarter(runtimeAdapter)
	if err := registry.Register(domain.JobTypeChannelCheckBinding, channelCheckHandler.Handle); err != nil {
		return fmt.Errorf("注册 channel_check_binding handler 失败: %w", err)
	}
	runtimeRefreshHandler := handlers.NewRuntimeRefreshStatusHandler(dbStore.Queries, runtimeAdapter)
	if err := registry.Register(domain.JobTypeRuntimeRefreshStatus, runtimeRefreshHandler.Handle); err != nil {
		return fmt.Errorf("注册 runtime_refresh_status handler 失败: %w", err)
	}
	healthCheckHandler := handlers.NewAppHealthCheckHandler(dbStore.Queries, runtimeAdapter)
	// 注入 container lifecycle:health check 发现容器已停(基础设施事件 / OOM /
	// docker daemon 重启等)时,在 restart budget 内主动 StartContainer 自愈,
	// 不再依赖用户手动重启或外部脚本拉起。
	healthCheckHandler.SetLifecycle(runtimeAdapter)
	if err := registry.Register(domain.JobTypeAppHealthCheck, healthCheckHandler.Handle); err != nil {
		return fmt.Errorf("注册 app_health_check handler 失败: %w", err)
	}
	if newapiFactory != nil {
		disableHandler := handlers.NewDisableAPIKeyHandler(dbStore.Queries, newapiFactory)
		if err := registry.Register(domain.JobTypeNewAPIDisableKey, disableHandler.Handle); err != nil {
			return fmt.Errorf("注册 newapi_disable_key handler 失败: %w", err)
		}
		restoreHandler := handlers.NewRestoreAPIKeyHandler(dbStore.Queries, newapiFactory)
		if err := registry.Register(domain.JobTypeNewAPIRestoreKey, restoreHandler.Handle); err != nil {
			return fmt.Errorf("注册 newapi_restore_key handler 失败: %w", err)
		}
	}

	jobWorker := worker.New(dbStore.Queries, redisQueue, registry, worker.Config{WorkerID: cfg.App.HTTPAddr})
	jobScheduler := scheduler.New(dbStore.Queries, redisQueue, scheduler.Config{})

	// HermesKanbanService / HermesCronService：通过 oc-ops 类型化 REST / SSE 提供看板与
	// Cron 管理能力，不再经 runtimeAdapter docker exec。两者共用 ocopsClient + ocopsResolver，
	// resolver 负责把 appID 解析为 oc-ops 坐标并判定 Supported（dev stub → UNSUPPORTED），
	// 读写权限由 service 层统一校验。
	hermesKanbanService := service.NewHermesKanbanService(ocopsClient, ocopsResolver)
	hermesCronService := service.NewHermesCronService(ocopsClient, ocopsResolver)

	server := &http.Server{
		Addr: cfg.App.HTTPAddr,
		Handler: api.NewRouter(api.Dependencies{
			AuthService:             authService,
			OrganizationService:     organizationService,
			ModelCatalogService:     modelCatalogService,
			MemberService:           memberService,
			OnboardingService:       onboardingService,
			AuditService:            auditService,
			RuntimeNodeService:      runtimeNodeService,
			ChannelService:          channelService,
			KnowledgeService:        knowledgeService,
			WorkspaceService:        workspaceService,
			RuntimeOpService:        runtimeOpService,
			ResourceMetricsService:  resourceMetricsService,
			AppService:              appService,
			UsageService:            usageService,
			RechargeService:         rechargeService,
			PlatformOverview:        platformOverviewService,
			AssistantVersionService: assistantVersionService,
			HermesKanbanService:     hermesKanbanService,
			HermesCronService:       hermesCronService,
			JobsStore:               dbStore.Queries,
			TokenManager:            tokenManager,
			AgentTokenSink:          agentTokenSink,
			EnrollmentSecret:        cfg.Runtime.EnrollmentSecret,
			JobNotifier:             redisQueue,
			AllowedOrigins:          allowedOriginsFromConfig(cfg),
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	rootCtx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pool := worker.NewPool(jobWorker, 4, 200*time.Millisecond)
	pool.SetLogger(logger)
	loop := scheduler.NewLoop(jobScheduler, 5*time.Second)
	loop.SetLogger(logger)

	nodeHealth := service.NewNodeHealthReconciler(dbStore.Queries, 90*time.Second)
	nodeHealthTask := service.NewPeriodicReconciler("runtime_node_health_reconcile", 30*time.Second, func(ctx context.Context) error {
		_, err := nodeHealth.Reconcile(ctx)
		return err
	})
	nodeProbe := service.NewRuntimeNodeProbeReconciler(
		dbStore.Queries,
		agentTokenResolver,
		agent.NewProbeClient(time.Duration(cfg.Runtime.Probe.TimeoutSeconds)*time.Second),
		service.RuntimeNodeProbeConfig{
			FailureThreshold:  int32(cfg.Runtime.Probe.FailureThreshold),
			RecoveryThreshold: int32(cfg.Runtime.Probe.RecoveryThreshold),
		},
	)
	nodeProbeTask := service.NewPeriodicReconciler("runtime_node_probe_reconcile", time.Duration(cfg.Runtime.Probe.IntervalSeconds)*time.Second, func(ctx context.Context) error {
		_, err := nodeProbe.Reconcile(ctx)
		return err
	})
	runtimeRefresh := newRuntimeRefreshDispatcher(dbStore.Queries, redisQueue)
	runtimeRefreshTask := service.NewPeriodicReconciler("runtime_refresh_status_dispatch", 30*time.Second, runtimeRefresh.Tick)
	healthCheckDisp := newHealthCheckDispatcher(dbStore.Queries, redisQueue)
	healthCheckTask := service.NewPeriodicReconciler("app_health_check_dispatch", 60*time.Second, healthCheckDisp.Tick)
	resourceCleanup := service.NewResourceSampleCleanup(dbStore.Queries)
	resourceCleanupTask := service.NewPeriodicReconciler("resource_sample_cleanup", time.Hour, func(ctx context.Context) error {
		_, _, err := resourceCleanup.RunOnce(ctx)
		return err
	})

	// ragflowParseStatusTask 周期把 RAGFlow 端的解析状态回写本地，
	// 取代旧"列表请求时同步刷新"的策略：无人浏览列表时状态也能收敛。
	// 仅在 RAGFlow 已配置时启用，避免 nil ragflowClient 导致 tick 空跑后还触发 panic。
	var ragflowParseStatusTask *service.PeriodicReconciler
	if ragflowClient != nil {
		ragflowParseStatusRefresher := service.NewRagflowParseStatusRefresher(dbStore.Queries, ragflowClient)
		ragflowParseStatusTask = service.NewPeriodicReconciler("ragflow_parse_status_refresh", 30*time.Second, ragflowParseStatusRefresher.Tick)
	}

	eg, gctx := errgroup.WithContext(rootCtx)

	eg.Go(func() error {
		logger.Info("manager api listening", "addr", cfg.App.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server 异常退出: %w", err)
		}
		return nil
	})
	eg.Go(func() error { return pool.Run(gctx) })
	eg.Go(func() error { return loop.Run(gctx) })

	// reaper 启动:周期 60s tick,扫 5 个 init 子状态下连续 90s 无更新的孤儿。
	// 多 manager 共存时通过 Redis 锁 ocm:reaper:lock 互斥;装配在 workerPool 启动之后,
	// 是因为多副本场景下"reaper 在 worker 之前完成"的串行约束本就拿不到,
	// 幂等性已由每阶段 phase* 函数保证(见 internal/worker/handlers/app_initialize.go)。
	// Start 内部自行 spawn goroutine 并监听 gctx.Done,errgroup 不需要再 Go 包一层。
	reaperInstance := reaper.New(
		dbStore.Queries, // *sqlc.Queries 已直接满足 reaper.Store(6 个方法 sqlc 生成齐全)
		redisQueue,      // redisQueue.Enqueue 满足 reaper.JobNotifier
		distLocker,
		uuid.NewString(),
		logger,
	)
	reaperInstance.Start(gctx)
	eg.Go(func() error { return nodeHealthTask.Run(gctx, logger) })
	eg.Go(func() error { return nodeProbeTask.Run(gctx, logger) })
	eg.Go(func() error { return runtimeRefreshTask.Run(gctx, logger) })
	eg.Go(func() error { return healthCheckTask.Run(gctx, logger) })
	eg.Go(func() error { return resourceCleanupTask.Run(gctx, logger) })
	if ragflowParseStatusTask != nil {
		eg.Go(func() error { return ragflowParseStatusTask.Run(gctx, logger) })
	}
	eg.Go(func() error {
		<-gctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		// HTTP server 单独使用独立 timeout，避免上游 ctx 已取消导致 Shutdown 无法给连接留清理时间。
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
		return nil
	})

	if err := eg.Wait(); err != nil {
		return err
	}
	return nil
}

// allowedOriginsFromConfig 从配置抽出 CORS 白名单。
// 当前只把 app.public_base_url 作为唯一允许 origin；空字符串视为不开启 CORS。
func allowedOriginsFromConfig(cfg config.Config) []string {
	if cfg.App.PublicBaseURL == "" {
		return nil
	}
	return []string{cfg.App.PublicBaseURL}
}

// hashPasswordWithDefault 使用默认 Argon2id 参数封装 auth.HashPassword，便于在 service 层注入。
func hashPasswordWithDefault(password string) (string, error) {
	return auth.HashPassword(password, auth.DefaultPasswordParams)
}

// hashTokenSHA256 用 SHA-256 对 agent token 做不可逆 hash 后存库。
// runtime node 的 token 不需要密码级强度，但必须保证泄露后也无法直接调用 manager API。
func hashTokenSHA256(token string) string { return auth.HashOpaqueToken(token) }

// autoMigrate 在 manager-api 启动早期把 schema 推到最新版本。
//
// 与 cmd/migrate 共用 internal/migrations 的 go:embed FS,保证迁移内容一致;
// golang-migrate 通过 PG advisory lock(全局 hash 锁)保证多副本同时启动时只有一个
// 真正跑迁移,其它实例阻塞等待锁,锁释放后命中 ErrNoChange 直接跳过。
//
// 失败语义为 fail-fast:返回 error 让 runManager 立即退出,
// 避免新 schema 字段缺失导致后续 sqlc 查询在运行时 panic。
// 大 schema 变更(锁全表 ALTER)的运维风险需要发版前评估,本函数不做特殊豁免。
func autoMigrate(databaseURL string, logger *slog.Logger) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("初始化迁移 source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, databaseURL)
	if err != nil {
		_ = src.Close()
		return fmt.Errorf("初始化迁移器: %w", err)
	}
	defer func() {
		sourceErr, databaseErr := m.Close()
		if sourceErr != nil {
			logger.Warn("关闭迁移 source 失败", "error", sourceErr)
		}
		if databaseErr != nil {
			logger.Warn("关闭迁移 database 失败", "error", databaseErr)
		}
	}()

	beforeVersion, beforeDirty, verErr := m.Version()
	if verErr != nil && !errors.Is(verErr, migrate.ErrNilVersion) {
		return fmt.Errorf("读取当前迁移版本: %w", verErr)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("执行 up 迁移: %w", err)
	}
	afterVersion, afterDirty, verErr := m.Version()
	if verErr != nil && !errors.Is(verErr, migrate.ErrNilVersion) {
		return fmt.Errorf("读取迁移后版本: %w", verErr)
	}
	logger.Info("启动迁移完成",
		"before_version", beforeVersion, "before_dirty", beforeDirty,
		"after_version", afterVersion, "after_dirty", afterDirty,
	)
	return nil
}
