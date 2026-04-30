package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"oc-manager/internal/api"
	"oc-manager/internal/auth"
	"oc-manager/internal/config"
	"oc-manager/internal/files"
	"oc-manager/internal/integrations/agent"
	"oc-manager/internal/integrations/channel"
	"oc-manager/internal/service"
	"oc-manager/internal/store"
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

	ctx := context.Background()
	dbStore, err := store.Open(ctx, cfg.Database.URL)
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}
	defer dbStore.Close()

	tokenManager, err := auth.NewTokenManager(
		cfg.Auth.JWTAccessSecret,
		cfg.Auth.JWTRefreshSecret,
		cfg.Auth.AccessTokenTTL.Duration,
		cfg.Auth.RefreshTokenTTL.Duration,
	)
	if err != nil {
		log.Fatalf("初始化认证令牌管理器失败: %v", err)
	}
	authService := service.NewAuthService(dbStore.Queries, tokenManager)
	organizationService := service.NewOrganizationService(dbStore.Queries)
	memberService := service.NewMemberService(dbStore.Queries, hashPasswordWithDefault)
	onboardingService := service.NewMemberOnboardingService(store.NewOnboardingRunner(dbStore), hashPasswordWithDefault)
	auditService := service.NewAuditService(dbStore.Queries)
	runtimeNodeStore := store.NewRuntimeNodeStore(dbStore)
	runtimeNodeService := service.NewRuntimeNodeService(runtimeNodeStore, hashTokenSHA256)
	channelRegistry := channel.NewRegistry()
	channelService := service.NewChannelService(dbStore.Queries, channelRegistry)
	knowledgeRoot := os.Getenv("OCM_KNOWLEDGE_ROOT")
	if knowledgeRoot == "" {
		knowledgeRoot = "/var/lib/oc-manager/knowledge"
	}
	safeRoot, err := files.NewSafeRoot(knowledgeRoot, 0)
	if err != nil {
		log.Fatalf("初始化知识库主副本失败: %v", err)
	}
	knowledgeService := service.NewKnowledgeService(files.NewKnowledgeMaster(safeRoot))
	runtimeOpService := service.NewRuntimeOperationService(dbStore.Queries)
	appService := service.NewAppService(dbStore.Queries)
	agentTokenResolver := agent.NewTokenResolver()

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
			JobsStore:           dbStore.Queries,
			TokenManager:        tokenManager,
			AgentTokenSink:      agentTokenResolver.Set,
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("manager api listening on %s", cfg.App.HTTPAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("启动 HTTP 服务失败: %v", err)
	}
}

// hashPasswordWithDefault 使用默认 Argon2id 参数封装 auth.HashPassword，便于在 service 层注入。
func hashPasswordWithDefault(password string) (string, error) {
	return auth.HashPassword(password, auth.DefaultPasswordParams)
}

// hashTokenSHA256 用 SHA-256 对 bootstrap/agent token 做不可逆 hash 后存库。
// runtime node 的 token 不需要密码级强度，但必须保证泄露后也无法直接调用 manager API。
func hashTokenSHA256(token string) string { return auth.HashOpaqueToken(token) }
