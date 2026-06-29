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
	// Captcha 是登录 PoW 验证码服务；nil 表示验证码关闭。
	Captcha *service.CaptchaService
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
	// IndustryKnowledgeUploadToken 是外部商业知识库上传行业文件的固定鉴权字符串；为空时外部上传入口返回 401。
	IndustryKnowledgeUploadToken string
	// TransferLimit 是 manager 文件上传下载的单请求限速配置；零值表示不限速。
	TransferLimit handlers.TransferLimitConfig
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
	// SkillTicketService 提供定制技能需求工单（提交/列表/详情/动作/报价/拒绝/角标）路由；nil 时不注册。
	SkillTicketService *service.SkillTicketService
	// SkillTicketMessageService 提供工单统一消息（text/image/file + 下载）路由；需与 SkillTicketService 同时注册。
	SkillTicketMessageService *service.SkillTicketMessageService
	// CustomSkillService 提供定制技能交付路由；nil 时不注册。
	CustomSkillService *service.CustomSkillService
	// PlatformOverview 提供平台总览路由。
	PlatformOverview *service.PlatformOverviewService
	// HermesKanbanService 提供实例任务看板能力；nil 时不注册 kanban 路由。
	HermesKanbanService *service.HermesKanbanService
	// HermesConversationService 提供实例会话能力；nil 时不注册会话路由。
	HermesConversationService *service.HermesConversationService
	// HermesConversationFileService 提供对话文件上传/下载能力；
	// 仅 S3 启用时非 nil，nil 时不注册文件路由（续聊含文件 part 将被拒）。
	HermesConversationFileService *service.ConversationFileService
	// HermesCronService 提供实例定时任务能力；nil 时不注册 cron 路由。
	HermesCronService *service.HermesCronService
	// AppSkillService 提供实例级 skill 安装/卸载/更新与对账能力；nil 时不注册 skill 路由。
	AppSkillService *service.AppSkillService
	// SkillLibraryService 提供 skill 市场聚合浏览/搜索（平台库 + ClawHub 公共库）；nil 时不注册。
	SkillLibraryService *service.SkillLibraryService
	// WebPublishConfigService 提供平台管理员对企业 web-publish 能力配置/开通/停用路由；nil 时不注册。
	WebPublishConfigService *service.WebPublishConfigService
	// WebPublishService 提供 app runtime token 驱动的站点发布与 site-server 同步能力；nil 时不注册同步端点。
	WebPublishService *service.WebPublishService
	// SiteSyncToken 是 site-server 轮询 /internal/web-publish/sites 时使用的共享鉴权 token；
	// 为空时同步端点不注册（防止未配置时意外开放）。
	SiteSyncToken string
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
	// DefaultLocale 是平台默认界面语言（来自 config i18n.default_locale），经公开配置端点下发给前端。
	DefaultLocale string
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
	// Locale 解析 Accept-Language 并归一到受支持语言，写入上下文供 apierror 按语言翻译错误消息。
	// defaultLocale 来自 config i18n.default_locale；deps 为空时回落 "en"。
	{
		defaultLocale := "en"
		if len(deps) > 0 && deps[0].DefaultLocale != "" {
			defaultLocale = deps[0].DefaultLocale
		}
		router.Use(middleware.Locale(defaultLocale))
	}
	// access log 挂在 RequestID 之后、鉴权/CSRF 之前：覆盖全部请求（含登录失败、CSRF
	// 拒绝、公共路由与未匹配 404），未鉴权导致的 4xx 也能记到；user_id 在 c.Next() 返回后
	// 从 c.Request.Context() 读取，此时 RequireUserAuth 已注入 principal，故仍能拿到。
	// 健康检查等纯噪音路径由中间件内部 skip 集合跳过。
	router.Use(middleware.AccessLog())
	// CSRF 双 submit cookie 校验：opt-in 模式（无 cookie 时放行），
	// 前端拿到 csrf_token cookie 后必须把它写到 X-CSRF-Token header 才能通过写操作。
	router.Use(middleware.RequireCSRF())

	// ── public：health（无需认证，无条件注册）─────────────────────────
	handlers.RegisterHealthRoutes(router)

	if len(deps) == 0 {
		return router
	}
	dep := deps[0]
	if dep.KnowledgeService != nil {
		industryHandler := handlers.NewIndustryKnowledgeHandler(dep.KnowledgeService, dep.IndustryKnowledgeUploadToken, dep.TransferLimit)
		handlers.RegisterExternalIndustryKnowledgeRoutes(router, industryHandler)
	}
	if dep.TokenManager == nil {
		// RequireUserAuth 依赖 TokenManager；无法初始化用户路由组，跳过全部业务路由。
		return router
	}

	// ── agent：runtime-agent 专用，agent enroll / heartbeat 路由已随节点服务移除 ──
	if dep.KnowledgeService != nil {
		handlers.RegisterRuntimeKnowledgeRoutes(router, handlers.NewRuntimeKnowledgeHandler(dep.KnowledgeService))
	}
	// runtime 发布端点：oc-publish 通过 X-OC-App-Token 上传站点 tar.gz；nil 时不注册。
	if dep.WebPublishService != nil {
		handlers.RegisterRuntimeWebPublishRoutes(router, handlers.NewRuntimeWebPublishHandler(dep.WebPublishService))
	}
	// /internal 组：pod 启动回调，不挂用户鉴权中间件，由 handler 内联校验 control token。
	if dep.BootstrapService != nil {
		handlers.RegisterBootstrapRoutes(router, handlers.NewBootstrapHandler(dep.BootstrapService))
	}
	// /internal/web-publish/sites：site-server 轮询同步端点，不走用户 JWT，
	// 由 handler 内联校验 X-OC-Site-Sync-Token；token 为空时不注册，防止误暴露。
	if dep.WebPublishService != nil && dep.SiteSyncToken != "" {
		handlers.RegisterInternalWebPublishRoutes(router, handlers.NewInternalWebPublishHandler(dep.WebPublishService, dep.SiteSyncToken))
	}

	// 公开前端配置：无需鉴权，登录页据此初始化界面语言。
	defaultLocale := dep.DefaultLocale
	if defaultLocale == "" {
		defaultLocale = "en"
	}
	handlers.RegisterPublicConfigRoutes(router, handlers.NewConfigHandler(defaultLocale, service.SupportedLocales))

	// ── user：RequireUserAuth 注入 principal，所有业务路由挂载在此组 ──
	user := router.Group("")
	user.Use(middleware.RequireUserAuth(dep.TokenManager))

	if dep.AuthService != nil {
		authHandler := handlers.NewAuthHandler(dep.AuthService, dep.Captcha)
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
		knowledgeHandler := handlers.NewKnowledgeHandler(dep.KnowledgeService, dep.TransferLimit)
		industryHandler := handlers.NewIndustryKnowledgeHandler(dep.KnowledgeService, dep.IndustryKnowledgeUploadToken, dep.TransferLimit)
		handlers.RegisterKnowledgeRoutes(user, knowledgeHandler)
		handlers.RegisterIndustryKnowledgeRoutes(user, industryHandler)
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
	if dep.SkillTicketService != nil && dep.SkillTicketMessageService != nil {
		handlers.RegisterSkillTicketRoutes(user, handlers.NewSkillTicketsHandler(dep.SkillTicketService, dep.SkillTicketMessageService))
	}
	if dep.CustomSkillService != nil {
		handlers.RegisterCustomSkillRoutes(user, handlers.NewCustomSkillsHandler(dep.CustomSkillService))
	}
	if dep.PlatformOverview != nil {
		handlers.RegisterPlatformOverviewRoutes(user, handlers.NewPlatformOverviewHandler(dep.PlatformOverview))
	}
	if dep.HermesKanbanService != nil {
		handlers.RegisterHermesKanbanRoutes(user, handlers.NewHermesKanbanHandler(dep.HermesKanbanService))
	}
	if dep.HermesConversationService != nil {
		handlers.RegisterHermesConversationRoutes(user, handlers.NewHermesConversationHandler(dep.HermesConversationService))
	}
	if dep.HermesConversationFileService != nil {
		handlers.RegisterHermesConversationFileRoutes(user, handlers.NewHermesConversationFileHandler(dep.HermesConversationFileService))
	}
	if dep.HermesCronService != nil {
		handlers.RegisterHermesCronRoutes(user, handlers.NewHermesCronHandler(dep.HermesCronService))
	}
	if dep.AppSkillService != nil {
		handlers.RegisterAppSkillRoutes(user, handlers.NewAppSkillsHandler(dep.AppSkillService))
	}
	if dep.SkillLibraryService != nil {
		handlers.RegisterSkillMarketRoutes(user, handlers.NewSkillMarketHandler(dep.SkillLibraryService))
	}
	if dep.WebPublishConfigService != nil {
		handlers.RegisterWebPublishConfigRoutes(user, handlers.NewWebPublishConfigHandler(dep.WebPublishConfigService))
	}
	return router
}
