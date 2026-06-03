package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/handlers"
	"oc-manager/internal/api/middleware"
	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// Dependencies 是 HTTP router 装配所需的可选依赖集合。
// nil service 表示对应路由组不注册，便于测试或最小模式只启用健康检查和部分 API。
type Dependencies struct {
	// AuthService 提供登录、刷新、登出和当前用户查询。
	AuthService *service.AuthService
	// OrganizationService 提供平台组织管理路由。
	OrganizationService *service.OrganizationService
	// ModelCatalogService 提供 new-api 实时模型列表路由。
	ModelCatalogService *service.ModelCatalogService
	// MemberService 提供组织成员 CRUD 与状态切换路由。
	MemberService *service.MemberService
	// OnboardingService 提供成员创建并初始化应用的事务路由。
	OnboardingService *service.MemberOnboardingService
	// AuditService 提供审计日志查询路由。
	AuditService *service.AuditService
	// ChannelService 提供渠道绑定与同步路由。
	ChannelService *service.ChannelService
	// KnowledgeService 提供组织和应用知识库路由。
	KnowledgeService *service.KnowledgeService
	// WorkspaceService 提供应用工作目录代理路由。
	WorkspaceService *service.WorkspaceService
	// UsageService 提供 new-api 用量代理路由。
	UsageService *service.UsageService
	// RuntimeOpService 提供应用运行时操作和 inspect 路由。
	RuntimeOpService *service.RuntimeOperationService
	// AppService 提供应用只读列表和详情路由。
	AppService *service.AppService
	// RechargeService 提供组织充值、充值记录和余额查询路由。
	RechargeService *service.RechargeService
	// AssistantVersionService 提供助手版本目录管理路由。
	AssistantVersionService *service.AssistantVersionService
	// PlatformSkillService 提供平台库 skill 管理路由；nil 时不注册。
	PlatformSkillService *service.PlatformSkillService
	// PlatformOverview 提供平台总览路由。
	PlatformOverview *service.PlatformOverviewService
	// HermesKanbanService 提供实例任务看板能力；nil 时不注册 kanban 路由。
	HermesKanbanService *service.HermesKanbanService
	// HermesCronService 提供实例定时任务能力；nil 时不注册 cron 路由。
	HermesCronService *service.HermesCronService
	// AppSkillService 提供实例级 skill 安装/卸载/更新与对账能力；nil 时不注册 skill 路由。
	AppSkillService *service.AppSkillService
	// BootstrapService 提供 pod 启动回调（/internal/apps/:id/bootstrap）；nil 时不注册。
	// /internal 组不挂用户鉴权中间件，由 handler 内联校验 control token。
	BootstrapService handlers.BootstrapAppService
	// JobsStore 提供按 job ID 查询异步任务状态的 handler 依赖。
	JobsStore handlers.JobsStore
	// TokenManager 供 RequireUserAuth 中间件验证 access token 并注入 principal。
	TokenManager *auth.TokenManager
	// JobNotifier 让 DeleteMember / 其它入队操作即时通知 Redis；nil 时退化到 scheduler 兜底。
	JobNotifier service.JobNotifier
	// AllowedOrigins 是 CORS 白名单。空切片代表同源部署不开 CORS。
	AllowedOrigins []string
}

// NewRouter 创建 Manager API 的 HTTP 路由。
// 路由分三组：
//   - public：无需认证（health + auth login/refresh/logout）
//   - internal：pod 启动回调专用（/internal/apps/:id/bootstrap），由 handler 内联校验 control token
//   - user：受 RequireUserAuth 中间件保护，所有业务 API
//
// handler 只负责 HTTP 协议层，业务权限、事务和外部系统副作用必须下沉到 service 层。
func NewRouter(deps ...Dependencies) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	if len(deps) > 0 && len(deps[0].AllowedOrigins) > 0 {
		// CORS 只在显式配置白名单时启用；同源部署保持最小响应头。
		router.Use(middleware.CORSAllowOrigin(deps[0].AllowedOrigins))
	}
	// RequestID 保证每个请求都携带 trace_id，供 slog ctx-aware 日志输出 trace_id 字段。
	router.Use(middleware.RequestID())
	// CSRF 双 submit cookie 校验：opt-in 模式（无 cookie 时放行），
	// 前端拿到 csrf_token cookie 后必须把它写到 X-CSRF-Token header 才能通过写操作。
	router.Use(middleware.RequireCSRF())

	// ── public：health（无需认证，无条件注册）─────────────────────────
	handlers.RegisterHealthRoutes(router)

	if len(deps) == 0 {
		return router
	}
	dep := deps[0]
	if dep.TokenManager == nil {
		// RequireUserAuth 依赖 TokenManager；无法初始化用户路由组，跳过全部业务路由。
		return router
	}

	// ── agent：runtime-agent 专用，agent enroll / heartbeat 路由已随节点服务移除 ──
	if dep.KnowledgeService != nil {
		handlers.RegisterRuntimeKnowledgeRoutes(router, handlers.NewRuntimeKnowledgeHandler(dep.KnowledgeService))
	}
	// /internal 组：pod 启动回调，不挂用户鉴权中间件，由 handler 内联校验 control token。
	if dep.BootstrapService != nil {
		handlers.RegisterBootstrapRoutes(router, handlers.NewBootstrapHandler(dep.BootstrapService))
	}

	// ── user：RequireUserAuth 注入 principal，所有业务路由挂载在此组 ──
	user := router.Group("")
	user.Use(middleware.RequireUserAuth(dep.TokenManager))

	if dep.AuthService != nil {
		authHandler := handlers.NewAuthHandler(dep.AuthService)
		// login/refresh/logout 不需要 Bearer token，注册到外层 router（public）。
		handlers.RegisterPublicAuthRoutes(router, authHandler)
		// /auth/me 需要已认证 principal，注册到 user 组。
		handlers.RegisterAuthMeRoutes(user, authHandler)
	}
	if dep.ModelCatalogService != nil {
		handlers.RegisterModelRoutes(user, handlers.NewModelsHandler(dep.ModelCatalogService))
	}
	if dep.OrganizationService != nil {
		handlers.RegisterOrganizationRoutes(user, handlers.NewOrganizationsHandler(dep.OrganizationService))
	}
	if dep.MemberService != nil {
		memberHandler := handlers.NewMembersHandler(dep.MemberService)
		if dep.OnboardingService != nil {
			memberHandler.SetOnboardingService(dep.OnboardingService)
		}
		if dep.JobNotifier != nil {
			memberHandler.SetJobNotifier(dep.JobNotifier)
		}
		handlers.RegisterMemberRoutes(user, memberHandler)
	}
	if dep.AuditService != nil {
		handlers.RegisterAuditRoutes(user, handlers.NewAuditHandler(dep.AuditService))
	}
	if dep.JobsStore != nil {
		handlers.RegisterJobsRoutes(user, handlers.NewJobsHandler(dep.JobsStore))
	}
	if dep.ChannelService != nil {
		handlers.RegisterChannelRoutes(user, handlers.NewChannelsHandler(dep.ChannelService))
	}
	if dep.KnowledgeService != nil {
		handlers.RegisterKnowledgeRoutes(user, handlers.NewKnowledgeHandler(dep.KnowledgeService))
	}
	if dep.WorkspaceService != nil {
		handlers.RegisterWorkspaceRoutes(user, handlers.NewWorkspaceHandler(dep.WorkspaceService))
	}
	if dep.UsageService != nil {
		handlers.RegisterUsageRoutes(user, handlers.NewUsageHandler(dep.UsageService))
	}
	if dep.RuntimeOpService != nil {
		handlers.RegisterAppRuntimeRoutes(user, handlers.NewAppRuntimeHandler(dep.RuntimeOpService))
	}
	if dep.AppService != nil {
		handlers.RegisterAppRoutes(user, handlers.NewAppsHandler(dep.AppService))
	}
	if dep.RechargeService != nil {
		handlers.RegisterRechargeRoutes(user, handlers.NewRechargeHandler(dep.RechargeService))
	}
	if dep.AssistantVersionService != nil {
		handlers.RegisterAssistantVersionRoutes(user, handlers.NewAssistantVersionsHandler(dep.AssistantVersionService))
	}
	if dep.PlatformSkillService != nil {
		handlers.RegisterPlatformSkillRoutes(user, handlers.NewPlatformSkillsHandler(dep.PlatformSkillService))
	}
	if dep.PlatformOverview != nil {
		handlers.RegisterPlatformOverviewRoutes(user, handlers.NewPlatformOverviewHandler(dep.PlatformOverview))
	}
	if dep.HermesKanbanService != nil {
		handlers.RegisterHermesKanbanRoutes(user, handlers.NewHermesKanbanHandler(dep.HermesKanbanService))
	}
	if dep.HermesCronService != nil {
		handlers.RegisterHermesCronRoutes(user, handlers.NewHermesCronHandler(dep.HermesCronService))
	}
	if dep.AppSkillService != nil {
		handlers.RegisterAppSkillRoutes(user, handlers.NewAppSkillsHandler(dep.AppSkillService))
	}
	return router
}
