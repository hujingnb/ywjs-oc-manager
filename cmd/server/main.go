// Package main 是 manager-api HTTP 服务入口。
//
// @title           Agent Runtime Manager API
// @version         1.0
// @description     基于 Hermes Agent runtime 的多组织管理后台 API。
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
	"oc-manager/internal/files"
	"oc-manager/internal/integrations/agent"
	"oc-manager/internal/integrations/channel"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/integrations/runtime"
	managerlog "oc-manager/internal/log"
	"oc-manager/internal/migrations"
	"oc-manager/internal/redis"
	"oc-manager/internal/runtime/imagecoord"
	"oc-manager/internal/runtime/imagesync"
	"oc-manager/internal/scheduler"
	"oc-manager/internal/service"
	"oc-manager/internal/store"
	"oc-manager/internal/worker"
	"oc-manager/internal/worker/handlers"
	"oc-manager/internal/worker/reaper"

	migrate "github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

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

	safeRoot, err := files.NewSafeRoot(cfg.App.KnowledgeRoot, 0)
	if err != nil {
		return fmt.Errorf("初始化知识库主副本失败: %w", err)
	}
	knowledgeMaster := files.NewKnowledgeMaster(safeRoot)
	knowledgeService := service.NewKnowledgeService(knowledgeMaster)
	// 同步状态服务：dispatcher 入队时写 pending、worker handler 完成时写 synced/failed、
	// API 层读取按 org 列出每节点状态供前端展示「重试同步」入口。
	knowledgeSyncStatusSvc := service.NewKnowledgeSyncStatusService(dbStore.Queries)
	// reload 协调器:knowledge 改动后给受影响 app 入 app_restart_container job
	// (带 in-memory debounce),使 Hermes 容器重启后真正读到新主副本。
	// 单 manager 实例:in-memory state 够用;多实例横向扩展时换 redis SETNX。
	knowledgeReloader := newKnowledgeReloadCoordinator(dbStore.Queries, redisQueue)
	knowledgeDispatcher := newKnowledgeSyncDispatcher(dbStore.Queries, redisQueue)
	knowledgeDispatcher.SetStatusMarker(knowledgeSyncStatusSvc)
	// 注入主副本读取能力:RetryOrgNode 用它扫所有文件做真正的"全量重推",
	// 不再是空有状态翻转的 noop。
	knowledgeDispatcher.SetKnowledgeReader(knowledgeMaster)
	knowledgeDispatcher.SetReloader(knowledgeReloader)
	knowledgeService.SetSyncDispatcher(knowledgeDispatcher)
	knowledgeService.SetSyncStatusSource(knowledgeSyncStatusSvc)
	knowledgeService.SetRetryDispatcher(knowledgeDispatcher)
	// dispatcher 入队失败时把错误写 audit_logs(target_type=knowledge_sync),
	// 让"主副本写成功但同步未入队"这类中间态可观测。
	knowledgeService.SetAuditor(auditService)
	appService := service.NewAppService(dbStore.Queries)
	appService.SetTxRunner(store.NewAppRunner(dbStore))
	appService.SetJobNotifier(redisQueue)
	runtimeOpService := service.NewRuntimeOperationService(dbStore.Queries, logger, redisQueue)
	resourceMetricsService := service.NewResourceMetricsService(dbStore.Queries)
	personaService := service.NewPersonaService(store.NewPersonaStore(dbStore))
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

	// LocalDockerSDKProvider 通过 /var/run/docker.sock 调 Docker Engine HTTP API,
	// 不依赖 manager 容器内的 docker CLI;凭据从挂载进来的宿主机 ~/.docker/config.json 读。
	// dockerHost 为空走 client.FromEnv(默认 unix:///var/run/docker.sock);
	// configPath 走容器内 /root/.docker/config.json(docker-compose 挂宿主机同名文件)。
	dockerSDK, err := imagesync.NewLocalDockerSDKProvider("", "/root/.docker/config.json")
	if err != nil {
		return fmt.Errorf("初始化本地 docker SDK provider: %w", err)
	}
	imageSync := imagesync.New(dockerSDK, nodeResolver)

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
	// agentClientAdapter(见 wiring.go)把 nodeResolver 返回的 imagesync.RemoteImageInfo
	// 转成 imagecoord.RemoteImageInfo,避免 imagecoord 包反向 import imagesync。
	imageCoord := imagecoord.NewCoordinator(
		dockerSDK,
		agentClientAdapter{inner: nodeResolver},
		distLocker,
		progressBus,
		uuid.NewString(),
	)
	runtimeAdapter := runtime.NewAgentBackedAdapter(nodeResolver, nodeResolver, imageSync)
	runtimeOpService.SetInspector(newRuntimeInspectorWrapper(runtimeAdapter))
	workspaceService := service.NewWorkspaceService(dbStore.Queries, runtimeAdapter, cfg.App.DataRoot)

	channelRegistry := channel.NewRegistry()
	channelService := service.NewChannelService(dbStore.Queries, channelRegistry, redisQueue)
	// channel 微信扫码 ExecAttach 是长连接(等用户扫码可达数分钟),
	// 必须用 streamingDockerResolver 拿无 timeout 的 docker client,
	// 否则 http.Client.Timeout=30s 会强制关闭 hijack 后的底层连接,
	// 导致 stream EOF + JSON 解析失败 + 容器内 oc-weixin-login.py 进程 orphan。
	wechatExecutor := channel.NewDockerExecutor(newStreamingDockerResolver(nodeResolver))
	wechatRunner := channel.NewDockerCommandRunner(wechatExecutor, newAppContainerLookup(dbStore.Queries))
	wechatResolver := channel.NewDockerBindingResolver(wechatExecutor)
	if err := channelRegistry.Register(channel.NewWeChatAdapter(wechatRunner)); err != nil {
		return fmt.Errorf("注册微信渠道失败: %w", err)
	}

	imageDistribution := newImageDistributorWrapper(service.NewImageDistributionService(imageSync))
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
	if modelCatalogService != nil {
		organizationService.SetModelValidator(modelCatalogService)
	}
	platformOverviewService := service.NewPlatformOverviewService(dbStore.Queries)

	registry := handlers.NewRegistry()
	// runtimeAdapter 同时实现 AgentDirInitializer / ContainerCreator / ContainerLifecycle
	// 三个接口（前者经 InitAppDirs 调用 agent file API，后两者经 docker proxy）。
	appInitHandler := handlers.NewAppInitializeHandler(
		dbStore.Queries,
		imageDistribution,
		appDirInitializerAdapter{adapter: runtimeAdapter},
		runtimeAdapter,
		runtimeAdapter,
		newapiFactory,
		handlers.AppInitializeConfig{
			RuntimeImage:         cfg.Hermes.RuntimeImage,
			SystemPromptTemplate: cfg.Hermes.SystemPromptTemplate,
			PlatformPrompt:       cfg.Hermes.SystemPromptTemplate,
			Cipher:               cipher,
			// DataDir 字段保留供其他特定场景使用；Hermes 文件分发已走 UploadAppRuntimeFile，
			// 不再在 manager 本机 DataDir 下写入配置文件。
			DataDir:           cfg.App.DataRoot,
			NewAPIBaseURL:     cfg.NewAPI.BaseURL,
			ContainerNetworks: cfg.Hermes.ContainerNetworks,
			LLM: handlers.AppInitializeLLMConfig{
				BaseURL:         cfg.Hermes.LLM.BaseURL,
				DefaultProvider: cfg.Hermes.LLM.DefaultProvider,
				DefaultModel:    cfg.Hermes.LLM.DefaultModel,
			},
			AuditHelper: appInitAuditHelper,
		},
	)
	// runtimeAdapter 同时实现 AppRuntimeFileWriter（UploadAppRuntimeFile），
	// 在多节点部署下把 Hermes 配置文件（SOUL.md/config.yaml/.env）上传到目标节点
	// agent 的 dataRoot/apps/<id>/.hermes/，确保 manager 与 docker daemon 可不同机。
	appInitHandler.SetRuntimeFileWriter(runtimeAdapter)
	// 注入主副本知识库读取能力：handler 在写完 SOUL.md/config.yaml/.env 后,
	// 遍历组织 + 应用知识库,把每个文件渲染成 .hermes/skills/kb-{org,app}-<slug>/SKILL.md,
	// Hermes 启动时按 skill 机制扫描该目录,使知识库内容进入 agent 上下文。
	appInitHandler.SetKnowledgeReader(knowledgeMaster)
	// 注入 ImageCoordinator:phasePull / phaseSync 通过 Redis 锁 + Pub/Sub 实现
	// 集群内 single-flight + 跨实例进度广播。未注入时 handler 会退回到旧的
	// ImageDistributor 路径(无单飞、本机直接 pull/sync),仅留给测试装配。
	appInitHandler.SetImageCoordinator(imageCoord)
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
	// 注入 config.yaml 重写器:UpdateModel 改 DB 后入队 app_restart_container,
	// restart handler 在 docker restart 之前重新渲染 config.yaml 并通过 agent 上传,
	// 让 Hermes 加载到 DB 最新的 model_id;不刷 .env(WEIXIN_* 由 channel bound 流管)
	// 也不刷 SOUL.md(persona prompt 由专用流程管)。
	restartHandler.SetConfigRefresher(newHermesConfigRefresher(dbStore.Queries, runtimeAdapter, cipher, knowledgeMaster, cfg.NewAPI.BaseURL, cfg.Hermes.SystemPromptTemplate))
	// 注入 session cleaner:restart 在容器实际重启前清 .hermes/sessions/,
	// 让 Hermes 启动新 session 时 snapshot 最新 SOUL.md(含改后的 model / persona /
	// 知识库等)。覆盖所有触发 restart 的入口(改 model / 重启 / persona 更新 / 未来其他)。
	restartHandler.SetSessionCleaner(runtimeAdapter)
	if err := registry.Register("app_restart_container", restartHandler.Handle); err != nil {
		return fmt.Errorf("注册 app_restart_container handler 失败: %w", err)
	}
	if err := registry.Register("app_delete", handlers.NewAppDeleteHandler(dbStore.Queries, runtimeAdapter, newapiFactory, nil).Handle); err != nil {
		return fmt.Errorf("注册 app_delete handler 失败: %w", err)
	}
	if err := registry.Register(domain.JobTypeChannelStartLogin, handlers.NewChannelStartLoginHandler(dbStore.Queries, channelRegistry).Handle); err != nil {
		return fmt.Errorf("注册 channel_start_login handler 失败: %w", err)
	}
	channelCheckHandler := handlers.NewChannelCheckBindingHandler(dbStore.Queries, channelRegistry, wechatResolver)
	channelCheckHandler.SetRuntimeFileWriter(runtimeAdapter)
	channelCheckHandler.SetRestarter(runtimeAdapter)
	channelCheckHandler.SetNewAPIBaseURL(cfg.NewAPI.BaseURL)
	// SetCipher 注入根密钥,bound 时用于解密 app.NewapiKeyCiphertext 取真实 OPENAI_API_KEY,
	// 确保重写 .env 时 OPENAI_API_KEY 是真实 token 而非空串。
	channelCheckHandler.SetCipher(cipher)
	if err := registry.Register(domain.JobTypeChannelCheckBinding, channelCheckHandler.Handle); err != nil {
		return fmt.Errorf("注册 channel_check_binding handler 失败: %w", err)
	}
	knowledgeSyncHandler := handlers.NewKnowledgeSyncHandler(knowledgeMaster, runtimeAdapter)
	knowledgeSyncHandler.SetStatusWriter(knowledgeSyncStatusSvc)
	// app scope 没有 knowledge_sync_status 表;把完成/失败事件落到 audit_logs
	// (target_type=app_knowledge_sync),前端审计页面可按 app_id 检索同步历史。
	knowledgeSyncHandler.SetAuditor(auditService)
	if err := registry.Register("knowledge_sync_node", knowledgeSyncHandler.Handle); err != nil {
		return fmt.Errorf("注册 knowledge_sync_node handler 失败: %w", err)
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

	server := &http.Server{
		Addr: cfg.App.HTTPAddr,
		Handler: api.NewRouter(api.Dependencies{
			AuthService:            authService,
			OrganizationService:    organizationService,
			ModelCatalogService:    modelCatalogService,
			MemberService:          memberService,
			OnboardingService:      onboardingService,
			AuditService:           auditService,
			RuntimeNodeService:     runtimeNodeService,
			ChannelService:         channelService,
			KnowledgeService:       knowledgeService,
			WorkspaceService:       workspaceService,
			RuntimeOpService:       runtimeOpService,
			ResourceMetricsService: resourceMetricsService,
			AppService:             appService,
			UsageService:           usageService,
			RechargeService:        rechargeService,
			PlatformOverview:       platformOverviewService,
			PersonaService:         personaService,
			JobsStore:              dbStore.Queries,
			TokenManager:           tokenManager,
			AgentTokenSink:         agentTokenSink,
			EnrollmentSecret:       cfg.Runtime.EnrollmentSecret,
			JobNotifier:            redisQueue,
			AllowedOrigins:         allowedOriginsFromConfig(cfg),
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
