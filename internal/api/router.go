package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/handlers"
	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/agent"
	"oc-manager/internal/service"
)

type Dependencies struct {
	AuthService         *service.AuthService
	OrganizationService *service.OrganizationService
	MemberService       *service.MemberService
	AuditService        *service.AuditService
	RuntimeNodeService  *service.RuntimeNodeService
	JobsStore           handlers.JobsStore
	TokenManager        *auth.TokenManager
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
		handlers.RegisterMemberRoutes(router, handlers.NewMembersHandler(dep.MemberService, dep.TokenManager))
	}
	if dep.AuditService != nil {
		handlers.RegisterAuditRoutes(router, handlers.NewAuditHandler(dep.AuditService, dep.TokenManager))
	}
	if dep.RuntimeNodeService != nil {
		handlers.RegisterRuntimeNodeRoutes(router, handlers.NewRuntimeNodesHandler(dep.RuntimeNodeService, dep.TokenManager))
		agent.RegisterRoutes(router, agent.NewEndpointsHandler(dep.RuntimeNodeService))
	}
	if dep.JobsStore != nil {
		handlers.RegisterJobsRoutes(router, handlers.NewJobsHandler(dep.JobsStore, dep.TokenManager))
	}
	return router
}
