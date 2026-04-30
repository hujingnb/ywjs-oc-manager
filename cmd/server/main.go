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
	"oc-manager/internal/files"
	"oc-manager/internal/integrations/agent"
	"oc-manager/internal/integrations/channel"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/integrations/runtime"
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
		configPath = "config/config.yaml"
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
	logger := log.New(logOut, "", log.LstdFlags)

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

	knowledgeRoot := os.Getenv("OCM_KNOWLEDGE_ROOT")
	if knowledgeRoot == "" {
		knowledgeRoot = "/var/lib/oc-manager/knowledge"
	}
	safeRoot, err := files.NewSafeRoot(knowledgeRoot, 0)
	if err != nil {
		return fmt.Errorf("初始化知识库主副本失败: %w", err)
	}
	knowledgeMaster := files.NewKnowledgeMaster(safeRoot)
	knowledgeService := service.NewKnowledgeService(knowledgeMaster)
	knowledgeService.SetSyncDispatcher(newKnowledgeSyncDispatcher(dbStore.Queries, redisQueue))
	appService := service.NewAppService(dbStore.Queries)
	runtimeOpService := service.NewRuntimeOperationService(dbStore.Queries, redisQueue)
	personaService := service.NewPersonaService(store.NewPersonaStore(dbStore))
	var rechargeService *service.RechargeService
	// runtime inspector 在 runtimeAdapter 构造之后注入；这里先声明字段，后面赋值。

	agentTokenResolver := agent.NewTokenResolver()
	nodeResolver := newNodeClientResolver(dbStore.Queries, agentTokenResolver)

	imageSync := imagesync.New(imagesync.LocalDockerCLIProvider{}, nodeResolver)
	runtimeAdapter := runtime.NewAgentBackedAdapter(nodeResolver, nodeResolver, imageSync)
	runtimeOpService.SetInspector(newRuntimeInspectorWrapper(runtimeAdapter))

	channelRegistry := channel.NewRegistry()
	channelService := service.NewChannelService(dbStore.Queries, channelRegistry)
	wechatRunner := channel.NewDockerCommandRunner(channel.NewDockerExecutor(nodeResolver), newAppContainerLookup(dbStore.Queries))
	if err := channelRegistry.Register(channel.NewWeChatAdapter(wechatRunner)); err != nil {
		return fmt.Errorf("注册微信渠道失败: %w", err)
	}

	imageDistribution := newImageDistributorWrapper(service.NewImageDistributionService(imageSync))
	var newapiClient *newapi.Client
	if cfg.NewAPI.BaseURL != "" {
		newapiClient = newapi.NewClient(cfg.NewAPI.BaseURL, cfg.NewAPI.AdminToken)
		rechargeService = service.NewRechargeService(dbStore.Queries, newapiClient)
	}

	registry := handlers.NewRegistry()
	appInitHandler := handlers.NewAppInitializeHandler(
		dbStore.Queries,
		imageDistribution,
		runtimeAdapter,
		newapiClient,
		handlers.AppInitializeConfig{
			RuntimeImage:         cfg.OpenClaw.RuntimeImage,
			SystemPromptTemplate: cfg.OpenClaw.SystemPromptTemplate,
			PlatformPrompt:       cfg.OpenClaw.SystemPromptTemplate,
			Cipher:               cipher,
		},
	)
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
	if err := registry.Register("app_delete", handlers.NewAppDeleteHandler(dbStore.Queries, runtimeAdapter, newapiClient, nil).Handle); err != nil {
		return fmt.Errorf("注册 app_delete handler 失败: %w", err)
	}
	knowledgeSyncHandler := handlers.NewKnowledgeSyncHandler(knowledgeMaster, runtimeAdapter)
	if err := registry.Register("knowledge_sync_node", knowledgeSyncHandler.Handle); err != nil {
		return fmt.Errorf("注册 knowledge_sync_node handler 失败: %w", err)
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
			RuntimeOpService:    runtimeOpService,
			AppService:          appService,
			RechargeService:     rechargeService,
			PersonaService:      personaService,
			JobsStore:           dbStore.Queries,
			TokenManager:        tokenManager,
			AgentTokenSink:      agentTokenResolver.Set,
			JobNotifier:         redisQueue,
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

// hashPasswordWithDefault 使用默认 Argon2id 参数封装 auth.HashPassword，便于在 service 层注入。
func hashPasswordWithDefault(password string) (string, error) {
	return auth.HashPassword(password, auth.DefaultPasswordParams)
}

// hashTokenSHA256 用 SHA-256 对 bootstrap/agent token 做不可逆 hash 后存库。
// runtime node 的 token 不需要密码级强度，但必须保证泄露后也无法直接调用 manager API。
func hashTokenSHA256(token string) string { return auth.HashOpaqueToken(token) }
