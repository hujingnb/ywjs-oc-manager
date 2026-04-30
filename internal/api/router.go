package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/handlers"
	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

type Dependencies struct {
	AuthService         *service.AuthService
	OrganizationService *service.OrganizationService
	MemberService       *service.MemberService
	OnboardingService   *service.MemberOnboardingService
	AuditService        *service.AuditService
	RuntimeNodeService  *service.RuntimeNodeService
	ChannelService      *service.ChannelService
	KnowledgeService    *service.KnowledgeService
	WorkspaceService    *service.WorkspaceService
	UsageService        *service.UsageService
	RuntimeOpService    *service.RuntimeOperationService
	AppService          *service.AppService
	RechargeService     *service.RechargeService
	PersonaService      *service.PersonaService
	JobsStore           handlers.JobsStore
	TokenManager        *auth.TokenManager
	// AgentTokenSink 在 agent register 成功时由 manager 进程缓存 (nodeID, agentToken)。
	// nil 时跳过缓存（仅供测试或未启用 docker proxy 的最小装配使用）。
	AgentTokenSink func(nodeID, agentToken string)
}

// NewRouter 创建 Manager API 的 HTTP 路由。
// handler 只负责 HTTP 协议层，业务权限、事务和外部系统副作用必须下沉到 service 层。
func NewRouter(deps ...Dependencies) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	handlers.RegisterHealthRoutes(router)
	if len(deps) == 0 {
		return router
	}
	dep := deps[0]
	if dep.TokenManager == nil {
		return router
	}
	if dep.AuthService != nil {
		handlers.RegisterAuthRoutes(router, handlers.NewAuthHandler(dep.AuthService, dep.TokenManager))
	}
	if dep.OrganizationService != nil {
		handlers.RegisterOrganizationRoutes(router, handlers.NewOrganizationsHandler(dep.OrganizationService, dep.TokenManager))
	}
	if dep.MemberService != nil {
		memberHandler := handlers.NewMembersHandler(dep.MemberService, dep.TokenManager)
		if dep.OnboardingService != nil {
			memberHandler.SetOnboardingService(dep.OnboardingService)
		}
		handlers.RegisterMemberRoutes(router, memberHandler)
	}
	if dep.AuditService != nil {
		handlers.RegisterAuditRoutes(router, handlers.NewAuditHandler(dep.AuditService, dep.TokenManager))
	}
	if dep.RuntimeNodeService != nil {
		handlers.RegisterRuntimeNodeRoutes(router, handlers.NewRuntimeNodesHandler(dep.RuntimeNodeService, dep.TokenManager))
		var agentHandler *handlers.AgentEndpointsHandler
		if dep.AgentTokenSink != nil {
			agentHandler = handlers.NewAgentEndpointsHandler(dep.RuntimeNodeService, dep.AgentTokenSink)
		} else {
			agentHandler = handlers.NewAgentEndpointsHandler(dep.RuntimeNodeService)
		}
		handlers.RegisterAgentRoutes(router, agentHandler)
	}
	if dep.JobsStore != nil {
		handlers.RegisterJobsRoutes(router, handlers.NewJobsHandler(dep.JobsStore, dep.TokenManager))
	}
	if dep.ChannelService != nil {
		handlers.RegisterChannelRoutes(router, handlers.NewChannelsHandler(dep.ChannelService, dep.TokenManager))
	}
	if dep.KnowledgeService != nil {
		handlers.RegisterKnowledgeRoutes(router, handlers.NewKnowledgeHandler(dep.KnowledgeService, dep.TokenManager))
	}
	if dep.WorkspaceService != nil {
		handlers.RegisterWorkspaceRoutes(router, handlers.NewWorkspaceHandler(dep.WorkspaceService, dep.TokenManager))
	}
	if dep.UsageService != nil {
		handlers.RegisterUsageRoutes(router, handlers.NewUsageHandler(dep.UsageService, dep.TokenManager))
	}
	if dep.RuntimeOpService != nil {
		handlers.RegisterAppRuntimeRoutes(router, handlers.NewAppRuntimeHandler(dep.RuntimeOpService, dep.TokenManager))
	}
	if dep.AppService != nil {
		handlers.RegisterAppRoutes(router, handlers.NewAppsHandler(dep.AppService, dep.TokenManager))
	}
	if dep.RechargeService != nil {
		handlers.RegisterRechargeRoutes(router, handlers.NewRechargeHandler(dep.RechargeService, dep.TokenManager))
	}
	if dep.PersonaService != nil {
		handlers.RegisterPersonaRoutes(router, handlers.NewPersonaHandler(dep.PersonaService, dep.TokenManager))
	}
	return router
}
