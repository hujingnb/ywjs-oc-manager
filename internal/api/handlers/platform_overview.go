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
}

type platformOverviewService interface {
	Get(ctx context.Context, principal auth.Principal) (service.PlatformOverview, error)
}

// NewPlatformOverviewHandler 创建 handler。
func NewPlatformOverviewHandler(svc platformOverviewService) *PlatformOverviewHandler {
	return &PlatformOverviewHandler{service: svc}
}

// RegisterPlatformOverviewRoutes 注册路由。
func RegisterPlatformOverviewRoutes(router gin.IRouter, handler *PlatformOverviewHandler) {
	router.GET("/api/v1/platform/overview", handler.Get)
}

// Get 返回平台总览。仅 platform_admin 可访问；service 层会再次校验。
//
// @Summary      平台总览
// @Description  平台管理员查询全平台的组织、成员、应用数量汇总
// @Tags         platform
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]service.PlatformOverview
// @Failure      401  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /platform/overview [get]
func (h *PlatformOverviewHandler) Get(c *gin.Context) {
	principal := principalFromCtx(c)
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
