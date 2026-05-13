package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// ModelsHandler 暴露 manager 代理的实时模型列表。
type ModelsHandler struct {
	// service 负责权限判断和实时目录读取，handler 只做 HTTP 映射。
	service modelService
	// tokens 校验 Bearer access token 并还原 Principal。
	tokens *auth.TokenManager
}

type modelService interface {
	List(ctx context.Context, principal auth.Principal) ([]service.ModelResult, error)
}

// NewModelsHandler 创建模型目录 HTTP handler。
func NewModelsHandler(svc modelService, tokens *auth.TokenManager) *ModelsHandler {
	return &ModelsHandler{service: svc, tokens: tokens}
}

// RegisterModelRoutes 注册模型目录路由。
func RegisterModelRoutes(router gin.IRouter, handler *ModelsHandler) {
	router.GET("/api/v1/models", handler.List)
}

// List 返回 new-api 当前可用模型列表。
//
// @Summary      模型列表
// @Description  平台管理员实时查询 new-api 当前可用模型，供组织模型 allowlist 使用
// @Tags         models
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string][]service.ModelResult
// @Failure      401  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /models [get]
func (h *ModelsHandler) List(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	models, err := h.service.List(c.Request.Context(), principal)
	if err != nil {
		if errors.Is(err, service.ErrForbidden) {
			c.JSON(http.StatusForbidden, gin.H{"error": "无权查看模型列表"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "模型列表暂时不可用"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": models})
}

func (h *ModelsHandler) principal(c *gin.Context) (auth.Principal, bool) {
	token, ok := bearerToken(c.GetHeader("Authorization"))
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "缺少访问令牌"})
		return auth.Principal{}, false
	}
	principal, err := h.tokens.VerifyAccessToken(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "访问令牌无效"})
		return auth.Principal{}, false
	}
	return principal, true
}
