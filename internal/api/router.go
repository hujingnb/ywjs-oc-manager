package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/handlers"
	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

type Dependencies struct {
	AuthService  *service.AuthService
	TokenManager *auth.TokenManager
}

// NewRouter 创建 Manager API 的 HTTP 路由。
// handler 只负责 HTTP 协议层，业务权限、事务和外部系统副作用必须下沉到 service 层。
func NewRouter(deps ...Dependencies) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	handlers.RegisterHealthRoutes(router)
	if len(deps) > 0 && deps[0].AuthService != nil && deps[0].TokenManager != nil {
		handlers.RegisterAuthRoutes(router, handlers.NewAuthHandler(deps[0].AuthService, deps[0].TokenManager))
	}
	return router
}
