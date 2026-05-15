package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// ChannelsHandler 处理应用渠道相关 HTTP 路由。
type ChannelsHandler struct {
	service channelService
}

type channelService interface {
	BeginAuth(ctx context.Context, principal auth.Principal, appID, channelType string) (service.ChallengeResult, error)
	PollAuth(ctx context.Context, principal auth.Principal, appID, channelType string) (service.ProgressResult, error)
	Unbind(ctx context.Context, principal auth.Principal, appID, channelType string) error
}

// NewChannelsHandler 创建 channel handler。
func NewChannelsHandler(svc channelService) *ChannelsHandler {
	return &ChannelsHandler{service: svc}
}

// RegisterChannelRoutes 注册渠道路由。
func RegisterChannelRoutes(router gin.IRouter, handler *ChannelsHandler) {
	group := router.Group("/api/v1/apps/:appId/channels/:channelType")
	group.POST("/auth", handler.BeginAuth)
	group.GET("/auth", handler.PollAuth)
	group.POST("/unbind", handler.Unbind)
}

// BeginAuth 触发渠道登录挑战。
//
// @Summary      触发渠道登录挑战
// @Description  为指定应用和渠道类型发起登录授权流程，返回挑战信息（如二维码 URL）
// @Tags         channels
// @Produce      json
// @Security     BearerAuth
// @Param        appId       path      string  true  "应用 ID"
// @Param        channelType path      string  true  "渠道类型（如 wechat）"
// @Success      200         {object}  map[string]service.ChallengeResult
// @Failure      401         {object}  ErrorResponse
// @Failure      403         {object}  ErrorResponse
// @Failure      404         {object}  ErrorResponse
// @Failure      500         {object}  ErrorResponse
// @Failure      503         {object}  ErrorResponse
// @Router       /apps/{appId}/channels/{channelType}/auth [post]
func (h *ChannelsHandler) BeginAuth(c *gin.Context) {
	principal := principalFromCtx(c)
	result, err := h.service.BeginAuth(c.Request.Context(), principal, c.Param("appId"), c.Param("channelType"))
	if err != nil {
		writeChannelError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"challenge": result})
}

// PollAuth 查询渠道登录进度。
//
// @Summary      查询渠道登录进度
// @Description  轮询指定应用渠道的登录授权进度，返回当前状态
// @Tags         channels
// @Produce      json
// @Security     BearerAuth
// @Param        appId       path      string  true  "应用 ID"
// @Param        channelType path      string  true  "渠道类型（如 wechat）"
// @Success      200         {object}  map[string]service.ProgressResult
// @Failure      401         {object}  ErrorResponse
// @Failure      403         {object}  ErrorResponse
// @Failure      404         {object}  ErrorResponse
// @Failure      500         {object}  ErrorResponse
// @Failure      503         {object}  ErrorResponse
// @Router       /apps/{appId}/channels/{channelType}/auth [get]
func (h *ChannelsHandler) PollAuth(c *gin.Context) {
	principal := principalFromCtx(c)
	result, err := h.service.PollAuth(c.Request.Context(), principal, c.Param("appId"), c.Param("channelType"))
	if err != nil {
		writeChannelError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"progress": result})
}

// Unbind 解绑渠道。
//
// @Summary      解绑渠道
// @Description  解除指定应用与渠道类型的绑定关系
// @Tags         channels
// @Produce      json
// @Security     BearerAuth
// @Param        appId       path      string  true  "应用 ID"
// @Param        channelType path      string  true  "渠道类型（如 wechat）"
// @Success      204         "解绑成功，无响应体"
// @Failure      401         {object}  ErrorResponse
// @Failure      403         {object}  ErrorResponse
// @Failure      404         {object}  ErrorResponse
// @Failure      500         {object}  ErrorResponse
// @Failure      503         {object}  ErrorResponse
// @Router       /apps/{appId}/channels/{channelType}/unbind [post]
func (h *ChannelsHandler) Unbind(c *gin.Context) {
	principal := principalFromCtx(c)
	if err := h.service.Unbind(c.Request.Context(), principal, c.Param("appId"), c.Param("channelType")); err != nil {
		writeChannelError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func writeChannelError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "无权操作渠道"})
	case errors.Is(err, service.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "应用或渠道绑定不存在"})
	case errors.Is(err, service.ErrChannelAdapterMissing):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "当前渠道未启用"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "渠道服务暂时不可用"})
	}
}
