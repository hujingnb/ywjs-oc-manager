// Package handlers - InternalWebPublishHandler 暴露 site-server 轮询用的内部同步端点。
// 路由 GET /internal/web-publish/sites 不挂用户 JWT 中间件，通过独立的
// X-OC-Site-Sync-Token header 鉴权（与 site-server MANAGER_SYNC_TOKEN 共享）。
// 返回契约 {"sites":[{"host","site_id","s3_prefix","status"}]}，与 Plan 3 site-server SiteRecord 对齐。
package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/service"
)

// syncService 是 InternalWebPublishHandler 需要的最小服务能力（窄接口，便于测试注入）。
type syncService interface {
	// ListActiveSitesForSync 返回所有 active 站点的路由摘要，供 site-server 同步。
	ListActiveSitesForSync(ctx context.Context) ([]service.SiteSyncRecord, error)
}

// InternalWebPublishHandler 暴露 site-server 轮询用的内部同步端点。
type InternalWebPublishHandler struct {
	service syncService
	// token 是与 site-server MANAGER_SYNC_TOKEN 共享的内部鉴权字符串；为空时一律 401。
	token string
}

// NewInternalWebPublishHandler 构造 handler。
func NewInternalWebPublishHandler(svc syncService, token string) *InternalWebPublishHandler {
	return &InternalWebPublishHandler{service: svc, token: token}
}

// RegisterInternalWebPublishRoutes 注册内部路由（集群内可达，独立 token，不走用户 JWT）。
// router 应为顶层 gin.Engine（不经过 RequireUserAuth 中间件），与 BootstrapRoutes 注册方式一致。
func RegisterInternalWebPublishRoutes(router gin.IRouter, h *InternalWebPublishHandler) {
	g := router.Group("/internal/web-publish")
	g.GET("/sites", h.ListSites)
}

// ListSites 校验 X-OC-Site-Sync-Token，返回所有 active 站点路由供 site-server 同步。
//
// 鉴权：header X-OC-Site-Sync-Token 必须与配置的 token 精确匹配；token 为空时一律 401，
// 防止未配置 token 的情况下开放端点。
//
// 响应契约：{"sites":[{"host":"...","site_id":"...","s3_prefix":"...","status":"..."}]}
func (h *InternalWebPublishHandler) ListSites(c *gin.Context) {
	// token 为空表示未配置，拒绝全部请求；token 不匹配也拒绝。
	if h.token == "" || c.GetHeader("X-OC-Site-Sync-Token") != h.token {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	recs, err := h.service.ListActiveSitesForSync(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sites": recs})
}
