// Package main 是 manager-api HTTP 服务入口。
//
// @title           OpenClaw Manager API
// @version         1.0
// @description     OpenClaw 多组织管理后台 API。
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
	"oc-manager/internal/redis"
	"oc-manager/internal/runtime/imagesync"
	"oc-manager/internal/scheduler"
	"oc-manager/internal/service"
	"oc-manager/internal/store"
	"oc-manager/internal/worker"
	"oc-manager/internal/worker/handlers"
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
	knowledgeDispatcher := newKnowledgeSyncDispatcher(dbStore.Queries, redisQueue)
	knowledgeDispatcher.SetStatusMarker(knowledgeSyncStatusSvc)
	knowledgeService.SetSyncDispatcher(knowledgeDispatcher)
	knowledgeService.SetSyncStatusSource(knowledgeSyncStatusSvc)
	knowledgeService.SetRetryDispatcher(knowledgeDispatcher)
	appService := service.NewAppService(dbStore.Queries)
	runtimeOpService := service.NewRuntimeOperationService(dbStore.Queries, logger, redisQueue)
	resourceMetricsService := service.NewResourceMetricsService(dbStore.Queries)
	personaService := service.NewPersonaService(store.NewPersonaStore(dbStore))
	// usage / organization service 在装配 newapi client 之后再实例化（见下方）；
	// 这里仅声明变量，真实赋值发生在 newapi wiring 段。
	var usageService *service.UsageService
	var organizationService *service.OrganizationService
	var rechargeService *service.RechargeService
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

	imageSync := imagesync.New(imagesync.LocalDockerCLIProvider{}, nodeResolver)
	runtimeAdapter := runtime.NewAgentBackedAdapter(nodeResolver, nodeResolver, imageSync)
	runtimeOpService.SetInspector(newRuntimeInspectorWrapper(runtimeAdapter))
	workspaceService := service.NewWorkspaceService(dbStore.Queries, runtimeAdapter, cfg.App.DataRoot)

	channelRegistry := channel.NewRegistry()
	channelService := service.NewChannelService(dbStore.Queries, channelRegistry, redisQueue)
	wechatExecutor := channel.NewDockerExecutor(nodeResolver)
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
		// 同时把 helper 暴露到 if 块外，给 AppInitializeConfig.AuditHelper 装配使用。
		appInitAuditHelper = newapiAuditHelper
	} else {
		// 未配 newapi：仍构造一个会在调用时 fail-soft 的 service（store/client 全 nil），
		// 保持 cmd/server 装配路径稳定，调用时返回 ErrUsageUnavailable / 创建组织报错。
		usageService = service.NewUsageService(nil, nil, nil)
		organizationService = service.NewOrganizationService(dbStore.Queries, nil, nil, nil)
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
			RuntimeImage:         cfg.OpenClaw.RuntimeImage,
			SystemPromptTemplate: cfg.OpenClaw.SystemPromptTemplate,
			PlatformPrompt:       cfg.OpenClaw.SystemPromptTemplate,
			Cipher:               cipher,
			ContainerNetworks:    cfg.OpenClaw.ContainerNetworks,
			LLM: handlers.AppInitializeLLMConfig{
				BaseURL:         cfg.OpenClaw.LLM.BaseURL,
				DefaultProvider: cfg.OpenClaw.LLM.DefaultProvider,
				DefaultModel:    cfg.OpenClaw.LLM.DefaultModel,
			},
			AuditHelper: appInitAuditHelper,
		},
	)
	// runtimeAdapter 同时实现 AppRuntimeFileWriter（UploadAppRuntimeFile），
	// 让 handler 在 InitAppDirs 之后把 pi-coding-agent settings.json 写到节点。
	appInitHandler.SetRuntimeFileWriter(runtimeAdapter)
	if err := registry.Register("app_initialize", appInitHandler.Handle); err != nil {
		return fmt.Errorf("注册 app_initialize handler 失败: %w", err)
	}
	if err := registry.Register("app_start_container", handlers.NewAppStartContainerHandler(dbStore.Queries, runtimeAdapter).Handle); err != nil {
		return fmt.Errorf("注册 app_start_container handler 失败: %w", err)
	}
	if err := registry.Register("app_stop_container", handlers.NewAppStopContainerHandler(dbStore.Queries, runtimeAdapter).Handle); err != nil {
		return fmt.Errorf("注册 app_stop_container handler 失败: %w", err)
	}
	if err := registry.Register("app_restart_container", handlers.NewAppRestartContainerHandler(dbStore.Queries, runtimeAdapter).Handle); err != nil {
		return fmt.Errorf("注册 app_restart_container handler 失败: %w", err)
	}
	if err := registry.Register("app_delete", handlers.NewAppDeleteHandler(dbStore.Queries, runtimeAdapter, newapiFactory, nil).Handle); err != nil {
		return fmt.Errorf("注册 app_delete handler 失败: %w", err)
	}
	if err := registry.Register(domain.JobTypeChannelStartLogin, handlers.NewChannelStartLoginHandler(dbStore.Queries, channelRegistry).Handle); err != nil {
		return fmt.Errorf("注册 channel_start_login handler 失败: %w", err)
	}
	if err := registry.Register(domain.JobTypeChannelCheckBinding, handlers.NewChannelCheckBindingHandler(dbStore.Queries, channelRegistry, wechatResolver).Handle); err != nil {
		return fmt.Errorf("注册 channel_check_binding handler 失败: %w", err)
	}
	knowledgeSyncHandler := handlers.NewKnowledgeSyncHandler(knowledgeMaster, runtimeAdapter)
	knowledgeSyncHandler.SetStatusWriter(knowledgeSyncStatusSvc)
	if err := registry.Register("knowledge_sync_node", knowledgeSyncHandler.Handle); err != nil {
		return fmt.Errorf("注册 knowledge_sync_node handler 失败: %w", err)
	}
	runtimeRefreshHandler := handlers.NewRuntimeRefreshStatusHandler(dbStore.Queries, runtimeAdapter)
	if err := registry.Register(domain.JobTypeRuntimeRefreshStatus, runtimeRefreshHandler.Handle); err != nil {
		return fmt.Errorf("注册 runtime_refresh_status handler 失败: %w", err)
	}
	healthCheckHandler := handlers.NewAppHealthCheckHandler(dbStore.Queries, runtimeAdapter)
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
	eg.Go(func() error { return nodeHealthTask.Run(gctx, logger) })
	eg.Go(func() error { return nodeProbeTask.Run(gctx, logger) })
	eg.Go(func() error { return runtimeRefreshTask.Run(gctx, logger) })
	eg.Go(func() error { return healthCheckTask.Run(gctx, logger) })
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
