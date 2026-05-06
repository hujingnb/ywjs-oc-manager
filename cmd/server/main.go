package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"oc-manager/internal/api"
	"oc-manager/internal/auth"
	"oc-manager/internal/config"
	"oc-manager/internal/domain"
	"oc-manager/internal/files"
	"oc-manager/internal/integrations/agent"
	"oc-manager/internal/integrations/channel"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/integrations/runtime"
	redactlog "oc-manager/internal/log"
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
		log.Fatalf("加载配置失败: %v", err)
	}
	if err := runManager(context.Background(), cfg, os.Stderr); err != nil {
		log.Fatalf("manager 退出: %v", err)
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
	// 把所有日志输出经过 RedactingWriter，避免密码 / token / sk- key 泄漏到容器日志或宿主 stdout。
	// 这里在 io.Writer 层包装，无需修改任何业务调用点。
	logger := log.New(redactlog.NewRedactingWriter(logOut), "", log.LstdFlags)

	masterKey, err := base64.StdEncoding.DecodeString(cfg.Security.MasterKey)
	if err != nil {
		return fmt.Errorf("master_key base64 解码失败: %w", err)
	}
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
	organizationService := service.NewOrganizationService(dbStore.Queries)
	memberService := service.NewMemberService(dbStore.Queries, hashPasswordWithDefault)
	onboardingService := service.NewMemberOnboardingService(store.NewOnboardingRunner(dbStore), hashPasswordWithDefault)
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
	runtimeOpService := service.NewRuntimeOperationService(dbStore.Queries, redisQueue)
	personaService := service.NewPersonaService(store.NewPersonaStore(dbStore))
	// platformOverviewService 在 usageService 后初始化（usage 在下方有 newapi 注入再覆盖）；
	// 这里先用 nil 占位，真实实例在 wiring 末尾装配。
	usageService := service.NewUsageService(nil)
	usageService.SetAppLister(appService)
	usageService.SetOrgLister(organizationService)
	var rechargeService *service.RechargeService
	// runtime inspector 在 runtimeAdapter 构造之后注入；这里先声明字段，后面赋值。

	agentTokenResolver := agent.NewTokenResolver()
	agentTokenStore := store.NewAgentTokenStore(dbStore)
	agentTokenResolver.SetPersistentLoader(newPersistentTokenLoader(agentTokenStore, cipher))
	agentTokenSink := func(nodeID, token string) {
		agentTokenResolver.Set(nodeID, token)
		// 加密入库；任何错误只走日志，不阻断 register 响应。
		if err := persistAgentToken(context.Background(), agentTokenStore, cipher, nodeID, token); err != nil {
			logger.Printf("持久化 agent token 失败 node=%s: %v", nodeID, err)
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
	// Sprint 2 集成 smoke 发现的 Go interface nil 陷阱：var newapiClient *newapi.Client
	// 在 cfg.NewAPI.BaseURL 为空时保持 nil，传给 handler 的 NewAPIClient interface 参数
	// 后会变成"interface 非 nil 但底层指针 nil"，handler 里 `if h.newapi == nil` 检查
	// 永远 false，CreateAPIKey 调用立刻 panic。
	//
	// 修法：用 handlers.NewAPIClient interface 类型变量保持，仅在真创建 client 时赋值，
	// 这样 NewAPI 未配置时变量保持 typed-nil interface（即 untyped nil），handler 检查通过。
	var newapiHandlerClient handlers.NewAPIClient
	var newapiClient *newapi.Client
	if cfg.NewAPI.BaseURL != "" {
		newapiClient = newapi.NewClient(cfg.NewAPI.BaseURL, cfg.NewAPI.AdminToken, cfg.NewAPI.AdminUserID)
		newapiHandlerClient = newapiClient
		rechargeService = service.NewRechargeService(dbStore.Queries, newapiClient)
		usageService = service.NewUsageService(newapiClient)
		usageService.SetAppLister(appService)
		usageService.SetOrgLister(organizationService)
	}
	platformOverviewService := service.NewPlatformOverviewService(dbStore.Queries, usageService)

	registry := handlers.NewRegistry()
	// runtimeAdapter 同时实现 AgentDirInitializer / ContainerCreator / ContainerLifecycle
	// 三个接口（前者经 InitAppDirs 调用 agent file API，后两者经 docker proxy）。
	appInitHandler := handlers.NewAppInitializeHandler(
		dbStore.Queries,
		imageDistribution,
		appDirInitializerAdapter{adapter: runtimeAdapter},
		runtimeAdapter,
		runtimeAdapter,
		newapiHandlerClient,
		handlers.AppInitializeConfig{
			RuntimeImage:         cfg.OpenClaw.RuntimeImage,
			SystemPromptTemplate: cfg.OpenClaw.SystemPromptTemplate,
			PlatformPrompt:       cfg.OpenClaw.SystemPromptTemplate,
			Cipher:               cipher,
			ContainerNetworks:    cfg.OpenClaw.ContainerNetworks,
			LLM: handlers.AppInitializeLLMConfig{
				BaseURL:            cfg.OpenClaw.LLM.BaseURL,
				DefaultProvider:    cfg.OpenClaw.LLM.DefaultProvider,
				DefaultModel:       cfg.OpenClaw.LLM.DefaultModel,
				OpenAICompatAPIKey: cfg.OpenClaw.LLM.OpenAICompat.APIKey,
			},
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
	// 同 newapiHandlerClient 的 typed-nil 防御：APIKeyDisabler interface 仅在真客户端可用时赋值。
	var apiKeyDisabler handlers.APIKeyDisabler
	if newapiClient != nil {
		apiKeyDisabler = newapiClient
	}
	if err := registry.Register("app_delete", handlers.NewAppDeleteHandler(dbStore.Queries, runtimeAdapter, apiKeyDisabler, nil).Handle); err != nil {
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
	healthCheckHandler := handlers.NewAppHealthCheckHandler(dbStore.Queries, runtimeAdapter, redisQueue)
	if err := registry.Register(domain.JobTypeAppHealthCheck, healthCheckHandler.Handle); err != nil {
		return fmt.Errorf("注册 app_health_check handler 失败: %w", err)
	}
	if newapiClient != nil {
		disableHandler := handlers.NewDisableAPIKeyHandler(dbStore.Queries, newapiClient)
		if err := registry.Register(domain.JobTypeNewAPIDisableKey, disableHandler.Handle); err != nil {
			return fmt.Errorf("注册 newapi_disable_key handler 失败: %w", err)
		}
		restoreHandler := handlers.NewRestoreAPIKeyHandler(dbStore.Queries, newapiClient)
		if err := registry.Register(domain.JobTypeNewAPIRestoreKey, restoreHandler.Handle); err != nil {
			return fmt.Errorf("注册 newapi_restore_key handler 失败: %w", err)
		}
	}

	jobWorker := worker.New(dbStore.Queries, redisQueue, registry, worker.Config{WorkerID: cfg.App.HTTPAddr})
	jobScheduler := scheduler.New(dbStore.Queries, redisQueue, scheduler.Config{})

	server := &http.Server{
		Addr: cfg.App.HTTPAddr,
		Handler: api.NewRouter(api.Dependencies{
			AuthService:         authService,
			OrganizationService: organizationService,
			MemberService:       memberService,
			OnboardingService:   onboardingService,
			AuditService:        auditService,
			RuntimeNodeService:  runtimeNodeService,
			ChannelService:      channelService,
			KnowledgeService:    knowledgeService,
			WorkspaceService:    workspaceService,
			RuntimeOpService:    runtimeOpService,
			AppService:          appService,
			UsageService:        usageService,
			RechargeService:     rechargeService,
			PlatformOverview:    platformOverviewService,
			PersonaService:      personaService,
			JobsStore:           dbStore.Queries,
			TokenManager:        tokenManager,
			AgentTokenSink:      agentTokenSink,
			JobNotifier:         redisQueue,
			AllowedOrigins:      allowedOriginsFromConfig(cfg),
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
	runtimeRefresh := newRuntimeRefreshDispatcher(dbStore.Queries, redisQueue)
	runtimeRefreshTask := service.NewPeriodicReconciler("runtime_refresh_status_dispatch", 30*time.Second, runtimeRefresh.Tick)
	healthCheckDisp := newHealthCheckDispatcher(dbStore.Queries, redisQueue)
	healthCheckTask := service.NewPeriodicReconciler("app_health_check_dispatch", 60*time.Second, healthCheckDisp.Tick)

	eg, gctx := errgroup.WithContext(rootCtx)

	eg.Go(func() error {
		logger.Printf("manager api listening on %s", cfg.App.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server 异常退出: %w", err)
		}
		return nil
	})
	eg.Go(func() error { return pool.Run(gctx) })
	eg.Go(func() error { return loop.Run(gctx) })
	eg.Go(func() error {
		return nodeHealthTask.Run(gctx, func(format string, args ...any) {
			logger.Printf(format, args...)
		})
	})
	eg.Go(func() error {
		return runtimeRefreshTask.Run(gctx, func(format string, args ...any) {
			logger.Printf(format, args...)
		})
	})
	eg.Go(func() error {
		return healthCheckTask.Run(gctx, func(format string, args ...any) {
			logger.Printf(format, args...)
		})
	})
	eg.Go(func() error {
		<-gctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
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

// hashTokenSHA256 用 SHA-256 对 bootstrap/agent token 做不可逆 hash 后存库。
// runtime node 的 token 不需要密码级强度，但必须保证泄露后也无法直接调用 manager API。
func hashTokenSHA256(token string) string { return auth.HashOpaqueToken(token) }
