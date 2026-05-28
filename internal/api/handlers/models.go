package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// ModelsHandler 暴露 manager 代理的实时模型列表。
type ModelsHandler struct {
	// service 负责权限判断和实时目录读取，handler 只做 HTTP 映射。
	service modelService
}

type modelService interface {
	List(ctx context.Context, principal auth.Principal) ([]service.ModelResult, error)
}

// NewModelsHandler 创建模型目录 HTTP handler。
func NewModelsHandler(svc modelService) *ModelsHandler {
	return &ModelsHandler{service: svc}
}

// RegisterModelRoutes 注册模型目录路由。
func RegisterModelRoutes(router gin.IRouter, handler *ModelsHandler) {
	router.GET("/api/v1/models", handler.List)
}

// List 返回 new-api 当前可用模型列表。
//
// @Summary      模型列表
// @Description  平台管理员实时查询 new-api 当前可用模型，供企业模型 allowlist 使用
// @Tags         models
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string][]service.ModelResult
// @Failure      401  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /models [get]
func (h *ModelsHandler) List(c *gin.Context) {
	principal := principalFromCtx(c)
	models, err := h.service.List(c.Request.Context(), principal)
	if err != nil {
		if errors.Is(err, service.ErrForbidden) {
			c.JSON(http.StatusForbidden, apierror.New("FORBIDDEN", "无权查看模型列表"))
			return
		}
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "模型列表暂时不可用"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": models})
}
