package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// PlatformOverviewHandler 暴露 /platform/overview。
type PlatformOverviewHandler struct {
	service platformOverviewService
	tokens  *auth.TokenManager
}

type platformOverviewService interface {
	Get(ctx context.Context, principal auth.Principal) (service.PlatformOverview, error)
}

// NewPlatformOverviewHandler 创建 handler。
func NewPlatformOverviewHandler(svc platformOverviewService, tokens *auth.TokenManager) *PlatformOverviewHandler {
	return &PlatformOverviewHandler{service: svc, tokens: tokens}
}

// RegisterPlatformOverviewRoutes 注册路由。
func RegisterPlatformOverviewRoutes(router gin.IRouter, handler *PlatformOverviewHandler) {
	router.GET("/api/v1/platform/overview", handler.Get)
}

// Get 返回平台总览。仅 platform_admin 可访问；service 层会再次校验。
func (h *PlatformOverviewHandler) Get(c *gin.Context) {
	token, ok := bearerToken(c.GetHeader("Authorization"))
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "缺少访问令牌"})
		return
	}
	principal, err := h.tokens.VerifyAccessToken(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "访问令牌无效"})
		return
	}
	view, err := h.service.Get(c.Request.Context(), principal)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrForbidden):
			c.JSON(http.StatusForbidden, gin.H{"error": "仅平台管理员可访问"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询平台总览失败"})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"overview": view})
}
