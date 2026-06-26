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
	apihandlers "oc-manager/internal/api/handlers"
	"oc-manager/internal/api/middleware"
	"oc-manager/internal/audit"
	"oc-manager/internal/auth"
	"oc-manager/internal/auth/pow"
	"oc-manager/internal/config"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/channel"
	"oc-manager/internal/integrations/clawhub"
	"oc-manager/internal/integrations/k8sorch"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/integrations/ocops"
	"oc-manager/internal/integrations/ragflow"
	"oc-manager/internal/integrations/storage"
	managerlog "oc-manager/internal/log"
	"oc-manager/internal/migrations"
	"oc-manager/internal/redis"
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
	logger := managerlog.NewSlogLogger(logOut, managerlog.ParseConfig(cfg.Logging.Level, cfg.Logging.Format))
	managerlog.SetRequestIDExtractor(middleware.RequestIDFromContext)
	slog.SetDefault(logger)
	// SQL 慢查询阈值同样来自 logging 段，在打开数据库前注入（store 包级阈值默认 200ms）。
	store.SetSlowQueryThreshold(time.Duration(cfg.Logging.SlowQueryMS) * time.Millisecond)

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

	// 传入平台默认语言，新增成员时继承创建者 locale，缺省回落此值（与 onboarding 路径一致）。
	memberService := service.NewMemberService(dbStore.Queries, hashPasswordWithDefault, cfg.I18n.DefaultLocale)
	// k8s 模型下不再需要选节点，pod 落点由调度器决定；直接构造 onboarding 服务。
	// 传入平台默认语言，创建实例时快照 owner 语言偏好，未设置时回退此默认值。
	onboardingService := service.NewMemberOnboardingService(store.NewOnboardingRunner(dbStore), hashPasswordWithDefault, cfg.I18n.DefaultLocale)
	auditService := service.NewAuditService(dbStore.Queries)

	var ragflowClient service.RAGFlowKnowledgeClient
	// ragflowHealClient 持有同一个 RAGFlow 具体客户端,供自愈任务使用。
	// RAGFlowKnowledgeClient 接口不含 StopParsing,而自愈卡死重置依赖它,故另存具体 *ragflow.Client;
	// 未配置 RAGFlow 时保持 nil,自愈任务据此不装配。
	var ragflowHealClient *ragflow.Client
	if cfg.RAGFlow.BaseURL != "" || cfg.RAGFlow.APIKey != "" {
		ragflowHTTPClient, err := ragflow.NewClient(cfg.RAGFlow.BaseURL, cfg.RAGFlow.APIKey, cfg.RAGFlow.RequestTimeout.Duration)
		if err != nil {
			return fmt.Errorf("初始化 RAGFlow 客户端失败: %w", err)
		}
		ragflowClient = ragflowHTTPClient
		ragflowHealClient = ragflowHTTPClient
	}
	knowledgeService := service.NewKnowledgeService(dbStore.Queries, ragflowClient)
	knowledgeService.SetDatasetChunkMethod(cfg.RAGFlow.ChunkMethod)
	knowledgeService.SetDefaultEmbeddingModel(cfg.RAGFlow.DefaultEmbeddingModel)
	knowledgeService.SetEmbeddingModelFallbacks(cfg.RAGFlow.EmbeddingModels)
	knowledgeService.SetTxRunner(store.NewKnowledgeRunner(dbStore))
	onboardingService.SetKnowledgeDatasetProvisioner(knowledgeService)
	appService := service.NewAppService(dbStore.Queries)
	appService.SetJobNotifier(redisQueue)
	// 注入版本镜像解析器：AppService 计算 version_synced 时需要把版本 image_id 解析成镜像 ref。
	appService.SetImageResolver(runtimeImageAdapter{images: cfg.Hermes.RuntimeImages})
	runtimeOpService := service.NewRuntimeOperationService(dbStore.Queries, logger, redisQueue)
	// usage / organization service 在装配 newapi client 之后再实例化（见下方）；
	// 这里仅声明变量，真实赋值发生在 newapi wiring 段。
	var usageService *service.UsageService
	var organizationService *service.OrganizationService
	var rechargeService *service.RechargeService
	var modelCatalogService *service.ModelCatalogService
	// distLocker 使用独立的 go-redis client，与 redisQueue 共享同一 Redis 物理实例但连接池分离；
	// 供 reaper 做跨实例分布式锁，防止多 manager 副本并发触发同一 reap 任务。
	imagecoordRedis := goredis.NewClient(&goredis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	defer imagecoordRedis.Close()
	distLocker := redis.NewRedisDistLocker(imagecoordRedis)

	// 验证码（登录 PoW）装配：仅 cfg.Captcha.Enabled 时构造，复用 imagecoordRedis
	// 这个已存在的 go-redis 客户端做一次性消费。
	var captchaService *service.CaptchaService
	if cfg.Captcha.Enabled {
		powVerifier := pow.NewVerifier(cfg.Captcha.HMACSecret, cfg.Captcha.Difficulty, cfg.Captcha.TTL.Duration)
		replayGuard := pow.NewRedisReplayGuard(imagecoordRedis, cfg.Redis.KeyPrefix)
		captchaService = service.NewCaptchaService(powVerifier, replayGuard)
	}
	// 注意 Go typed-nil 接口陷阱：把具体 *CaptchaService(可能为 nil) 直接赋给
	// CaptchaVerifier 接口，会得到非 nil 接口，导致 AuthService.captcha != nil 误判 panic。
	// 故关闭时显式保持 nil 接口。
	var captchaVerifier service.CaptchaVerifier
	if captchaService != nil {
		captchaVerifier = captchaService
	}
	authService := service.NewAuthService(dbStore.Queries, tokenManager, captchaVerifier)

	// k8s 编排器：启用 k8s 时构造 client-go clientset + KubernetesAdapter，取代 docker 编排。
	// 未启用时 orch 为 nil，生命周期/init handler 已做 nil 守卫（降级：无法管理 app，仅最小运行）。
	var orch k8sorch.Orchestrator
	if cfg.Kubernetes.Enabled {
		cs, err := k8sorch.NewClientset(cfg.Kubernetes.Kubeconfig)
		if err != nil {
			return fmt.Errorf("初始化 k8s clientset 失败: %w", err)
		}
		orch = k8sorch.NewKubernetesAdapter(cs, cfg.Kubernetes.Namespace)
	}
	// k8sInitCfg 供 app_initialize 渲染 AppSpec 使用，从 cfg.Kubernetes 提取最小子集。
	k8sInitCfg := handlers.AppInitializeK8sConfig{
		OpsImage:         cfg.Kubernetes.OpsImage,
		BootstrapBaseURL: cfg.Kubernetes.BootstrapBaseURL,
		ImagePullSecret:  cfg.Kubernetes.ImagePullSecret,
		Resources: handlers.AppInitializeK8sResources{
			Requests: handlers.AppInitializeK8sResourceSpec{CPU: cfg.Kubernetes.Resources.Requests.CPU, Memory: cfg.Kubernetes.Resources.Requests.Memory},
			Limits:   handlers.AppInitializeK8sResourceSpec{CPU: cfg.Kubernetes.Resources.Limits.CPU, Memory: cfg.Kubernetes.Resources.Limits.Memory},
		},
		Proxy: handlers.AppInitializeK8sProxy{
			HTTPProxy:  cfg.Kubernetes.PodProxy.HTTPProxy,
			HTTPSProxy: cfg.Kubernetes.PodProxy.HTTPSProxy,
			NoProxy:    cfg.Kubernetes.PodProxy.NoProxy,
		},
	}

	// oc-ops HTTP 客户端 + app 坐标解析器：cron / kanban / 微信扫码登录均走
	// oc-ops 类型化 REST / SSE 长连接。
	// 30s 超时仅约束普通 RPC（DoJSON）；SSE 长连接（kanban watch / 微信扫码，
	// pod 侧 qr_login 超时达 480s）由 ocops.Client 内部的无 Timeout streamHTTP 执行、
	// 生命周期靠调用方 ctx 控制——http.Client.Timeout 会一并中断 Body 读取，不能用于
	// SSE 流式订阅（需靠 ctx cancel 而非 http 超时来终止）。
	ocopsClient := ocops.NewClient(&http.Client{Timeout: 30 * time.Second})
	// ocopsResolver 把 appID 解析为 oc-ops 调用坐标。
	// spec-A 已落地真实 k8s 寻址：基址即 render.go 渲染的 oc-ops Service DNS
	// （serviceName=app-<id>-ocops），namespace 跟随 cfg.Kubernetes.Namespace 参数化
	// （为空回退 oc-apps）；per-app OC_OPS_TOKEN 由 resolver 从 runtime_token_ciphertext
	// 经 cipher 解密注入，不再是占位。
	ocopsNamespace := cfg.Kubernetes.Namespace
	if ocopsNamespace == "" {
		ocopsNamespace = "oc-apps"
	}
	ocopsBaseURLTpl := fmt.Sprintf("http://app-%%s-ocops.%s.svc:8080", ocopsNamespace)
	ocopsResolver := service.NewOcOpsResolverFromStore(dbStore.Queries, cipher, ocopsBaseURLTpl)
	// AppLocaleStatus 需实时查 oc-ops 实例当前语言：注入 oc-ops 客户端与坐标解析器。
	// 二者在此处才装配完成（appService 早于此构造），故用 setter 补注入。
	appService.SetOcOps(ocopsClient, ocopsResolver)

	channelRegistry := channel.NewRegistry()
	channelService := service.NewChannelService(dbStore.Queries, channelRegistry, redisQueue)
	// 飞书手填模式需加密 app_secret 后入库，复用进程内同一份 master_key 的 cipher。
	channelService.SetCipher(cipher)
	// 微信扫码登录走 oc-ops HTTP SSE：runner 持 ocopsClient（满足 hermes.ChannelLoginStreamer），
	// 每次登录按 AuthInput.Endpoint（worker 经 ocopsResolver 解析后注入）路由到目标实例。
	wechatRunner := channel.NewDockerCommandRunner(ocopsClient)
	// OcOpsBindingResolver 通过 oc-ops ChannelStatus 查询微信绑定身份（AccountID），
	// 取代旧 DockerBindingResolver（docker exec 读 plugin state 文件），spec-A 改造点。
	// ocopsBindingLocationResolver 在 main 包适配 service.OcOpsResolver→channel.OcOpsLocationResolver，
	// 避免 channel 包直接依赖 service 包（循环依赖）。
	wechatResolver := channel.NewOcOpsBindingResolver(ocopsClient, ocopsBindingLocationResolver{inner: ocopsResolver})
	if err := channelRegistry.Register(channel.NewWeChatAdapter(wechatRunner)); err != nil {
		return fmt.Errorf("注册微信渠道失败: %w", err)
	}

	// 飞书渠道：扫码注册 SSE + 手填 probe 都经 oc-ops；runner/prober 适配 ocopsClient。
	feishuAdapter := channel.NewFeishuAdapter(channel.NewOcOpsFeishuRunner(ocopsClient))
	feishuAdapter.SetProber(channel.NewOcOpsFeishuProber(ocopsClient))
	if err := channelRegistry.Register(feishuAdapter); err != nil {
		return fmt.Errorf("注册飞书渠道失败: %w", err)
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
	// bootstrapSvc 仅在 S3 启用时赋值（bootstrap 依赖对象存储 + skill 预签名）；
	// nil 时 router 不注册 /internal 路由，符合最小模式预期。
	var bootstrapSvc *service.BootstrapService
	// workspaceObjStore 供 WorkspaceService 浏览 app workspace；S3 未启用时为 nil，
	// Task 14 将完整接入；此处 nil 时 service 层返回 ErrWorkspaceMissing。
	var workspaceObjStore storage.ObjectStore
	// workspacePresignTTL 为 workspace 文件下载预签名 URL 有效期；S3 启用时从配置读取。
	workspacePresignTTL := 15 * time.Minute
	if cfg.Storage.S3.Enabled {
		s3cfg := storage.S3Config{
			Endpoint:        cfg.Storage.S3.Endpoint,
			Region:          cfg.Storage.S3.Region,
			Bucket:          cfg.Storage.S3.Bucket,
			AccessKeyID:     cfg.Storage.S3.AccessKeyID,
			SecretAccessKey: cfg.Storage.S3.SecretAccessKey,
			UsePathStyle:    cfg.Storage.S3.UsePathStyle,
		}
		objStore := storage.NewS3ObjectStore(s3cfg)
		// s3Skills 供 BootstrapService 生成 skill 预签名读 URL；
		// 助手版本 skill 已改为从平台库选（快照引用库路径），不再需要独立 tar 写入副本。
		s3Skills := service.NewS3SkillBlobStore(objStore, cfg.Storage.S3.PresignTTL.Duration)
		// bootstrap 服务依赖对象存储（restore 预签名）+ skill 预签名 + 长期写凭证。
		// 目标对象存储不支持标准 STS AssumeRole，故 sidecar 写回直接复用 manager 长期凭证。
		bootstrapSvc = service.NewBootstrapService(
			dbStore.Queries,
			cipher,
			objStore,
			s3Skills,
			service.BootstrapConfig{
				Endpoint:         cfg.Storage.S3.Endpoint,
				Region:           cfg.Storage.S3.Region,
				Bucket:           cfg.Storage.S3.Bucket,
				AccessKeyID:      cfg.Storage.S3.AccessKeyID,
				SecretAccessKey:  cfg.Storage.S3.SecretAccessKey,
				NewAPIBaseURL:    cfg.NewAPI.BaseURL,
				KnowledgeBaseURL: cfg.Hermes.ManagerRuntimeBaseURL,
				PlatformPrompt:   cfg.Hermes.SystemPromptTemplate,
				PresignTTL:       cfg.Storage.S3.PresignTTL.Duration,
			},
		)
		// workspace 数据读 S3（spec-A2a），与 bootstrap 共用同一 objStore 实例
		workspaceObjStore = objStore
		workspacePresignTTL = cfg.Storage.S3.PresignTTL.Duration
	}
	// libraryBlobs 是平台库 skill 归档的存储后端：
	// S3 启用时另建一个 ObjectStore 实例（与上方 bootstrap/workspace 用的 objStore 同配置同桶但相互独立，
	// 因 objStore 作用域限于上方 if 块），否则退回本地 FS（与 skillBlobStore 同根）。
	var libraryBlobs service.LibraryBlobStore
	if cfg.Storage.S3.Enabled {
		s3cfg := storage.S3Config{
			Endpoint:        cfg.Storage.S3.Endpoint,
			Region:          cfg.Storage.S3.Region,
			Bucket:          cfg.Storage.S3.Bucket,
			AccessKeyID:     cfg.Storage.S3.AccessKeyID,
			SecretAccessKey: cfg.Storage.S3.SecretAccessKey,
			UsePathStyle:    cfg.Storage.S3.UsePathStyle,
		}
		libraryBlobs = service.NewS3LibraryBlobStore(storage.NewS3ObjectStore(s3cfg))
	} else {
		libraryBlobs = service.NewFSLibraryBlobStore(cfg.App.DataRoot)
	}
	// 知识库分片上传依赖：S3 启用时注入对象存储 multipart 能力 + Redis 会话存储（复用 imagecoordRedis），
	// 让大文件走分片上传规避公网入口超时；未启用 S3 时不注入，分片上传不可用、前端回退直传。
	if cfg.Storage.S3.Enabled {
		s3cfg := storage.S3Config{
			Endpoint:        cfg.Storage.S3.Endpoint,
			Region:          cfg.Storage.S3.Region,
			Bucket:          cfg.Storage.S3.Bucket,
			AccessKeyID:     cfg.Storage.S3.AccessKeyID,
			SecretAccessKey: cfg.Storage.S3.SecretAccessKey,
			UsePathStyle:    cfg.Storage.S3.UsePathStyle,
		}
		uploadSessions := service.NewRedisKnowledgeUploadSessions(imagecoordRedis, cfg.Redis.KeyPrefix, 24*time.Hour)
		// partSize 传 0 用 service 默认 8MB。
		knowledgeService.SetMultipartUploader(storage.NewS3ObjectStore(s3cfg), uploadSessions, 0)
	}
	// archiveCache 是第三方市场归档读穿缓存，市场下载与（间接）安装/版本共用：复用 libraryBlobs 同一对象存储。
	archiveCache := service.NewSkillArchiveCache(libraryBlobs)
	platformSkillService := service.NewPlatformSkillService(dbStore.Queries, libraryBlobs)
	// 定制技能工单 service：提交工单时主表与首条需求消息必须同事务落库，避免半成品工单。
	skillTicketService := service.NewSkillTicketServiceWithTx(dbStore.Queries, store.NewSkillTicketRunner(dbStore))
	// 工单消息 service：text/image/file 统一消息流,文件内容复用 libraryBlobs 的 ticket-message 前缀。
	skillTicketMessageService := service.NewSkillTicketMessageService(dbStore.Queries, libraryBlobs)
	// 定制技能交付 service：解析扁平 tar、写归档与 custom_skills、置工单 delivered；dbStore.Queries 满足 CustomSkillStore，归档落 libraryBlobs。
	customSkillService := service.NewCustomSkillService(dbStore.Queries, libraryBlobs)
	workspaceService := service.NewWorkspaceService(dbStore.Queries, workspaceObjStore, workspacePresignTTL)

	// ClawHub 公共库客户端：BaseURL 为空则保持 nil，不接入 ClawHub（市场仅平台库，
	// per-app 安装与更新检测仅走平台来源）。
	// 使用局部变量 *clawhub.ClawHubClient（具体指针类型），避免赋值给接口类型产生
	// "nil 指针包装成非 nil interface"的陷阱；各处注入均通过条件判断确保仅在非 nil 时赋值。
	var clawhubClient *clawhub.ClawHubClient
	if cfg.ClawHub.BaseURL != "" {
		clawhubClient = clawhub.NewClient(cfg.ClawHub.BaseURL, cfg.ClawHub.RequestTimeout.Duration)
	}

	// 助手版本 service：镜像来自配置、模型校验走 new-api 目录、skill 从平台库选（dbStore.Queries 满足 PlatformSkillLibrary）。
	// modelCatalogService 为 nil 时（未配 newapi）跳过构造，路由自动不注册。
	var assistantVersionService *service.AssistantVersionService
	if modelCatalogService != nil {
		assistantVersionService = service.NewAssistantVersionService(
			store.NewAssistantVersionStore(dbStore),
			runtimeImageAdapter{images: cfg.Hermes.RuntimeImages},
			modelValidatorAdapter{catalog: modelCatalogService},
			dbStore.Queries,
			nil, // clawhub：下方按 clawhubClient 非 nil 时回填，避免 nil *Client 包装成非 nil interface
			libraryBlobs,
		)
		assistantVersionService.SetTxRunner(store.NewAssistantVersionRunner(dbStore))
		// 仅当 clawhubClient 指针非 nil 时注入 clawhub 下载器（与 AppSkillService 同一守卫）。
		if clawhubClient != nil {
			assistantVersionService.SetClawHubDownloader(clawhubClient)
		}
		// 助手版本服务作为组织 allowlist 校验器：组织创建/编辑时校验所选版本 id 都存在。
		organizationService.SetVersionValidator(assistantVersionService)
	}
	organizationService.SetKnowledgeDatasetProvisioner(knowledgeService)
	platformOverviewService := service.NewPlatformOverviewService(dbStore.Queries)

	registry := handlers.NewRegistry()
	// app 初始化 handler 走 k8s 路径：编排能力经 SetOrchestrator 注入（见下方），
	// 构造期只需 store / newapiFactory / 渲染配置。
	appInitHandler := handlers.NewAppInitializeHandler(
		dbStore.Queries,
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
			AuditHelper:       appInitAuditHelper,
			ResolveRuntimeImage: func(imageID string) (string, bool) {
				return config.ResolveRuntimeImage(cfg.Hermes.RuntimeImages, imageID)
			},
			ManagerRuntimeBaseURL: cfg.Hermes.ManagerRuntimeBaseURL,
		},
	)
	// 注入真实 k8s 编排器与渲染配置：phaseCreate/phaseStart 据此 EnsureApp + WaitReady，
	// 把 app 渲染成 Deployment + Service + Secret 并等待 pod Ready。orch 为 nil（未启用 k8s）
	// 时 handler 内部跳过这两阶段。
	appInitHandler.SetOrchestrator(orch, k8sInitCfg)
	// 注入版本 skill 种子注入 store：初始化时把版本 skills_json 里实例尚无的 skill 写入
	// app_skills，供 bootstrap 后续为 pod 提供运行时 skill 下载 URL。
	appInitHandler.SetSeedStore(dbStore.Queries)
	if err := registry.Register("app_initialize", appInitHandler.Handle); err != nil {
		return fmt.Errorf("注册 app_initialize handler 失败: %w", err)
	}
	// 生命周期 handler 走 k8s 编排（appOrchestrator + ObjectStore）：传入上方构造的真实 orch
	// （未启用 k8s 时为 nil，handler 内部已做守卫）。
	// workspaceObjStore 在 S3 启用时已有值（供 workspace + bootstrap），复用给 lifecycle handler。
	if err := registry.Register("app_start_container", handlers.NewAppStartContainerHandler(dbStore.Queries, orch).Handle); err != nil {
		return fmt.Errorf("注册 app_start_container handler 失败: %w", err)
	}
	if err := registry.Register("app_stop_container", handlers.NewAppStopContainerHandler(dbStore.Queries, orch).Handle); err != nil {
		return fmt.Errorf("注册 app_stop_container handler 失败: %w", err)
	}
	// restart 走 k8s 编排：传入真实 orch，workspaceObjStore 供 S3 归档/恢复。
	restartHandler := handlers.NewAppRestartContainerHandler(dbStore.Queries, orch, workspaceObjStore)
	// 注入 input refresher：restart 刷新版本配置并检测镜像变更，bootstrap 接管 pod 启动配置后
	// refresher 的节点文件写入逻辑保留兼容，镜像 ref 比较由 refresher 返回值驱动。
	// k8s 下 pod 配置由 bootstrap 在启动时交付，restart 不再向节点写 manifest；
	// refresher 只解析绑定版本的镜像 ref 与 revision，供镜像变更检测使用。
	restartHandler.SetInputRefresher(newAppInputRefresher(
		dbStore.Queries,
		func(imageID string) (string, bool) {
			return config.ResolveRuntimeImage(cfg.Hermes.RuntimeImages, imageID)
		},
	))
	// 注入 job notifier：restart 检测到镜像变更时入队 app_initialize job 后即时唤醒 worker。
	restartHandler.SetJobNotifier(redisQueue)
	// 注入镜像不变重启分支的版本 skill 种子注入 store：Scale(1) 成功后补齐版本新增 skill，
	// 确保重启后 bootstrap 能为 pod 提供完整 skill 列表。
	restartHandler.SetRestartSeedStore(dbStore.Queries)
	if err := registry.Register("app_restart_container", restartHandler.Handle); err != nil {
		return fmt.Errorf("注册 app_restart_container handler 失败: %w", err)
	}
	// app_delete 走 k8s 编排：传入真实 orch 删除 Deployment/Service/Secret，
	// workspaceObjStore 供删除前 S3 归档，knowledgeService 清理 RAGFlow 数据集。
	if err := registry.Register("app_delete", handlers.NewAppDeleteHandler(dbStore.Queries, orch, newapiFactory, workspaceObjStore, knowledgeService).Handle); err != nil {
		return fmt.Errorf("注册 app_delete handler 失败: %w", err)
	}
	if err := registry.Register(domain.JobTypeChannelStartLogin, handlers.NewChannelStartLoginHandler(dbStore.Queries, channelRegistry, ocopsEndpointResolver{resolver: ocopsResolver}).Handle); err != nil {
		return fmt.Errorf("注册 channel_start_login handler 失败: %w", err)
	}
	channelCheckHandler := handlers.NewChannelCheckBindingHandler(dbStore.Queries, channelRegistry, wechatResolver)
	// 渠道绑定后重载 hermes platform：经 Orchestrator.RolloutRestart 重建 pod（spec-A2b 落地）。
	channelCheckHandler.SetRestarter(orchChannelRestarter{orch: orch})
	// 飞书两阶段 check 依赖：凭证注入 app Secret（PatchSecretKeys 仅 *KubernetesAdapter 实现，
	// Orchestrator 接口未暴露，按需类型断言取出；未启用 k8s 时 patcher 留 nil，飞书注入降级）、
	// cipher 把 secret 加密落 metadata、health 客户端经 endpoint resolver 查 oc-ops 飞书连通态。
	var feishuPatcher handlers.FeishuSecretPatcher
	if p, ok := orch.(handlers.FeishuSecretPatcher); ok {
		feishuPatcher = p
	}
	feishuHealth := handlers.NewOcOpsFeishuHealthClient(ocopsEndpointResolver{resolver: ocopsResolver}, ocopsClient)
	channelCheckHandler.SetFeishuDeps(feishuPatcher, cipher, feishuHealth)
	if err := registry.Register(domain.JobTypeChannelCheckBinding, channelCheckHandler.Handle); err != nil {
		return fmt.Errorf("注册 channel_check_binding handler 失败: %w", err)
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
	// Cron 管理能力。两者共用 ocopsClient + ocopsResolver，
	// resolver 负责把 appID 解析为 oc-ops 坐标并判定 Supported（dev stub → UNSUPPORTED），
	// 读写权限由 service 层统一校验。
	hermesKanbanService := service.NewHermesKanbanService(ocopsClient, ocopsResolver)
	hermesConversationService := service.NewHermesConversationService(ocopsClient, ocopsResolver)
	hermesCronService := service.NewHermesCronService(ocopsClient, ocopsResolver)

	// AppSkillService：实例级 skill 安装/卸载/更新与对账。
	// - AppLocator 由 ocopsResolver.LocateApp 满足（已在 ocops.go 实现，含 VersionID 字段）。
	// - AssistantVersionLoader 由 assistantVersionSkillLoader 适配（GetAssistantVersion + decodeSkills）。
	// - ClawHub downloader 注入真实 clawhubClient（BaseURL 为空时为 nil，service 层对 nil 有守卫）。
	//   注意 nil interface 陷阱：ClawHub 字段类型为 ClawHubDownloader（接口），直接传 nil *Client
	//   会产生"非 nil interface 包装 nil 指针"的错误；通过条件赋值确保仅在指针非 nil 时赋值给接口。
	// - audit 由 auditService 直接满足（*AuditService.Record 签名与 AuditRecorder 接口对齐）。
	avSkillLoader := service.NewAssistantVersionSkillLoader(dbStore.Queries)
	appSkillDeps := service.AppSkillServiceDeps{
		Store:    dbStore.Queries,
		Apps:     ocopsResolver,
		Versions: avSkillLoader,
		Platform: platformSkillService,
		// Custom 接入定制技能取装来源：安装链路命中 custom source-ref 时由它返回归档与 sha。
		Custom: customSkillService,
		// ClawHub 默认 nil：BaseURL 为空时不注入，避免 nil interface 陷阱（见上注释）。
		ClawHub: nil,
		Blobs:   libraryBlobs,
		OcOps:   ocopsClient,
		Audit:   auditService,
	}
	// 仅当 clawhubClient 指针非 nil 时才赋值给接口字段，防止 nil *Client 包装成非 nil interface。
	if clawhubClient != nil {
		appSkillDeps.ClawHub = clawhubClient
	}
	appSkillService := service.NewAppSkillService(appSkillDeps)

	// SkillUpdateChecker 定时回源检测 skill 最高版本，写回 app_skills.latest_version。
	// SkillUpdateCheckerPlatformStore 由 dbStore.Queries 满足（ListPlatformSkills）。
	// SkillUpdateCheckerAppSkillStore 由 dbStore.Queries 满足（ListDistinctAppSkillSources / ListAppSkillsBySourceRef / UpdateAppSkillLatest）。
	// ClawHub 版本列表：ClawHubVersionLister 接口要求 ListVersions 返回 []service.SkillVersion，
	// 而 clawhub.ClawHubClient.ListVersions 返回 []clawhub.SkillVersion（不同包类型，无法直接赋值）；
	// 通过 clawhubVersionListerAdapter 包装做类型转换，仅在 clawhubClient 非 nil 时注入。
	var skillUpdateCheckerClawHub service.ClawHubVersionLister
	if clawhubClient != nil {
		skillUpdateCheckerClawHub = clawhubVersionListerAdapter{client: clawhubClient}
	}
	// 第三参 custom store 同样由 dbStore.Queries 满足（实现 ListAllCustomSkills），
	// 使更新检测覆盖 custom 来源；这是最终接线，Task 7 无需再改本处。
	skillUpdateChecker := service.NewSkillUpdateChecker(dbStore.Queries, dbStore.Queries, dbStore.Queries, skillUpdateCheckerClawHub)
	// skillUpdateCheckerTask 的 PeriodicReconciler 装配下移到 errgroup + leaderElector 就绪之后,
	// 以便用 onlyLeader 把 tick gate 到 leader 副本(见下方装配段)。

	// 市场聚合：平台库来源（复用 platformSkillService）+ 可选 ClawHub 公共库来源。
	// clawhubSource 为 nil 时市场仅展示平台库（降级），不影响安装/更新等其他功能。
	// 注意：NewSkillLibraryService 第二参数类型为 SkillSource 接口；
	// clawhubSource 声明为接口类型，nil 值安全（不会产生 nil pointer wrapped in interface）。
	platformSource := service.NewPlatformSource(platformSkillService)
	var clawhubSource service.SkillSource
	if clawhubClient != nil {
		// imagecoordRedis 已在上方构造（与 distLocker 共用同一 Redis 物理实例），复用避免新建连接。
		clawhubSource = service.NewClawHubSource(clawhubClient, imagecoordRedis, cfg.ClawHub.CacheTTL.Duration, archiveCache)
	}
	// custom 来源：按主体可见性过滤定制技能（all_org / org_admins / requester_only），接入市场聚合。
	// dbStore.Queries 满足 CustomSourceStore（ListVisibleCustomSkills 等）。
	customSource := service.NewCustomSource(dbStore.Queries)
	skillLibraryService := service.NewSkillLibraryService(platformSource, clawhubSource, customSource)

	transferLimit := apihandlers.TransferLimitConfig{
		UploadBytesPerSec:   cfg.TransferLimit.UploadBytesPerSec,
		DownloadBytesPerSec: cfg.TransferLimit.DownloadBytesPerSec,
	}

	server := &http.Server{
		Addr: cfg.App.HTTPAddr,
		Handler: api.NewRouter(api.Dependencies{
			AuthService:                  authService,
			Captcha:                      captchaService,
			OrganizationService:          organizationService,
			ModelCatalogService:          modelCatalogService,
			MemberService:                memberService,
			OnboardingService:            onboardingService,
			AuditService:                 auditService,
			ChannelService:               channelService,
			KnowledgeService:             knowledgeService,
			IndustryKnowledgeUploadToken: cfg.IndustryKnowledge.UploadToken,
			TransferLimit:                transferLimit,
			WorkspaceService:             workspaceService,
			RuntimeOpService:             runtimeOpService,
			AppService:                   appService,
			UsageService:                 usageService,
			RechargeService:              rechargeService,
			PlatformOverview:             platformOverviewService,
			AssistantVersionService:      assistantVersionService,
			PlatformSkillService:         platformSkillService,
			SkillTicketService:           skillTicketService,
			SkillTicketMessageService:    skillTicketMessageService,
			CustomSkillService:           customSkillService,
			HermesKanbanService:          hermesKanbanService,
			HermesConversationService:    hermesConversationService,
			HermesCronService:            hermesCronService,
			AppSkillService:              appSkillService,
			SkillLibraryService:          skillLibraryService,
			BootstrapService:             bootstrapSvc,
			JobsStore:                    dbStore.Queries,
			TokenManager:                 tokenManager,
			JobNotifier:                  redisQueue,
			AllowedOrigins:               allowedOriginsFromConfig(cfg),
			DefaultLocale:                cfg.I18n.DefaultLocale,
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	rootCtx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pool := worker.NewPool(jobWorker, 4, 200*time.Millisecond)
	pool.SetLogger(logger)
	loop := scheduler.NewLoop(jobScheduler, 5*time.Second)
	loop.SetLogger(logger)

	// app 状态 poll reconciler：周期对运行中 app 调 Orchestrator.Status 同步 pod 状态到 DB，
	// 取代 docker inspect 健康自愈（manager 不自愈，崩溃重启交 Deployment 控制器）。
	// 仅在启用 k8s（orch != nil）时挂载；未启用时不跑空 tick。
	// reconciler 对象在此构造,但 PeriodicReconciler 任务装配下移到 leaderElector 就绪后,
	// 以便用 onlyLeader 把 tick gate 到 leader 副本。
	var appStatusReconciler *service.AppStatusReconciler
	if orch != nil {
		// redisQueue 作 jobEnqueuer：reconciler 兜底恢复（error 但 pod 已 Ready）重新入队 init job 后通知 scheduler。
		appStatusReconciler = service.NewAppStatusReconciler(dbStore.Queries, orch, redisQueue)
	}
	// spec-A2b：node_resource_samples / instance_resource_samples 已删，ResourceSampleCleanup 不再装配。

	// ragflowParseStatusRefresher 周期把 RAGFlow 端的解析状态回写本地，
	// 取代旧"列表请求时同步刷新"的策略：无人浏览列表时状态也能收敛。
	// 仅在 RAGFlow 已配置时启用，避免 nil ragflowClient 导致 tick 空跑后还触发 panic。
	// 同上,任务装配下移到 leaderElector 就绪后。
	var ragflowParseStatusRefresher *service.RagflowParseStatusRefresher
	if ragflowClient != nil {
		ragflowParseStatusRefresher = service.NewRagflowParseStatusRefresher(dbStore.Queries, ragflowClient)
	}

	eg, gctx := errgroup.WithContext(rootCtx)

	// 所有定时任务只在 leader 副本运行,避免多副本重复轮询/重复自愈。
	// leaderElector 基于已有 distLocker(Redis 锁)选出单一 leader,token 用本副本启动时生成的 UUID,
	// 租约 30s、续租间隔 10s(<lease,避免续租间隙过期抖动);Run 阻塞直到 gctx 取消,故挂在同一 errgroup。
	leaderElector := service.NewLeaderElector(distLocker, cfg.Redis.KeyPrefix+"scheduler:leader", uuid.NewString(), 30*time.Second, 10*time.Second)
	eg.Go(func() error { return leaderElector.Run(gctx, logger) })

	// onlyLeader 包装 reconciler 的 fn:非 leader 直接跳过本轮,只有 leader 副本真正执行 tick。
	onlyLeader := func(fn func(ctx context.Context) error) func(ctx context.Context) error {
		return func(ctx context.Context) error {
			if !leaderElector.IsLeader() {
				return nil
			}
			return fn(ctx)
		}
	}

	// 在 leaderElector/onlyLeader 就绪后装配各 PeriodicReconciler,统一 gate 到 leader。
	// app 状态同步任务仅在启用 k8s(appStatusReconciler != nil)时装配。
	var appStatusTask *service.PeriodicReconciler
	if appStatusReconciler != nil {
		appStatusTask = service.NewPeriodicReconciler("app_status_reconcile", 15*time.Second, onlyLeader(appStatusReconciler.Tick))
	}
	// RAGFlow 解析状态回写任务仅在 RAGFlow 已配置(ragflowParseStatusRefresher != nil)时装配。
	var ragflowParseStatusTask *service.PeriodicReconciler
	if ragflowParseStatusRefresher != nil {
		ragflowParseStatusTask = service.NewPeriodicReconciler("ragflow_parse_status_refresh", 30*time.Second, onlyLeader(ragflowParseStatusRefresher.Tick))
	}
	// RAGFlow 解析异常自愈:全库失败文件重解析 + 卡死 running 文件 stop→reparse;与刷新任务并列,同样 gate 到 leader。
	var ragflowHealTask *service.PeriodicReconciler
	if ragflowParseStatusRefresher != nil { // 与刷新任务同条件:ragflowClient 已配置
		healState := service.NewHealState(imagecoordRedis, cfg.Redis.KeyPrefix, service.HealStateTTL{
			Attempts: 6 * time.Hour,
			Giveup:   7 * 24 * time.Hour,
		})
		healer := service.NewRagflowAnomalyHealer(dbStore.Queries, ragflowHealClient, healState, service.HealerConfig{
			MaxAttempts: cfg.RAGFlow.SelfHeal.MaxAttempts,
			// Backoffs[n-1] 是第 n 次尝试后的冷却:默认 MaxAttempts=3 时只发生「第1次后 0、第2次后 10m」两段,
			// 第3次即给上、不再冷却,故只需两档(backoffFor 对超出索引会 clamp 到末档,调大 MaxAttempts 仍安全)。
			Backoffs:       []time.Duration{0, 10 * time.Minute},
			StuckThreshold: cfg.RAGFlow.SelfHeal.StuckThreshold.Duration,
			BatchLimit:     int32(cfg.RAGFlow.SelfHeal.BatchLimit),
		})
		healer.SetLogger(logger)
		ragflowHealTask = service.NewPeriodicReconciler("ragflow_self_heal", cfg.RAGFlow.SelfHeal.Interval.Duration, onlyLeader(healer.Tick))
	}
	// skill 更新检测任务:每 30 分钟回源查最高版本,写回 app_skills.latest_version,同样 gate 到 leader。
	skillUpdateCheckerTask := service.NewPeriodicReconciler("skill_update_check", 30*time.Minute, onlyLeader(skillUpdateChecker.Tick))

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
	if appStatusTask != nil {
		eg.Go(func() error { return appStatusTask.Run(gctx, logger) })
	}
	if ragflowParseStatusTask != nil {
		eg.Go(func() error { return ragflowParseStatusTask.Run(gctx, logger) })
	}
	if ragflowHealTask != nil {
		eg.Go(func() error { return ragflowHealTask.Run(gctx, logger) })
	}
	// skill 更新检测定时任务：每 30 分钟回源查最高版本，写回 app_skills.latest_version。
	eg.Go(func() error { return skillUpdateCheckerTask.Run(gctx, logger) })
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
