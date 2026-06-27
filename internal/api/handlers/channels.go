package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// ChannelsHandler 处理应用渠道相关 HTTP 路由。
type ChannelsHandler struct {
	service channelService
}

type channelService interface {
	BeginAuth(ctx context.Context, principal auth.Principal, appID, channelType string) (service.ChallengeResult, error)
	// BeginFeishuAuth 是飞书专用发起入口（仅扫码自动创建，入参只含 domain）。
	BeginFeishuAuth(ctx context.Context, principal auth.Principal, appID string, in service.FeishuAuthInput) (service.ChallengeResult, error)
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
// @Description  为指定应用和渠道类型发起登录授权流程，返回挑战信息（如二维码 URL）。feishu 渠道需传请求体，其他渠道（如 wechat）无需请求体。
// @Tags         channels
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId       path      string                   true   "应用 ID"
// @Param        channelType path      string                   true   "渠道类型（如 wechat、feishu）"
// @Param        body        body      FeishuChannelAuthRequest false  "飞书渠道发起请求体（仅 feishu 渠道）"
// @Success      200         {object}  map[string]service.ChallengeResult
// @Failure      400         {object}  ErrorResponse
// @Failure      401         {object}  ErrorResponse
// @Failure      403         {object}  ErrorResponse
// @Failure      404         {object}  ErrorResponse
// @Failure      409         {object}  ErrorResponse
// @Failure      500         {object}  ErrorResponse
// @Failure      503         {object}  ErrorResponse
// @Router       /apps/{appId}/channels/{channelType}/auth [post]
func (h *ChannelsHandler) BeginAuth(c *gin.Context) {
	principal := principalFromCtx(c)
	appID := c.Param("appId")
	channelType := c.Param("channelType")

	// 飞书走专用入口（读请求体 domain，仅扫码自动创建），与微信等渠道分流。
	if channelType == domain.ChannelTypeFeishu {
		var req FeishuChannelAuthRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apierror.JSON(c, http.StatusBadRequest, "BAD_REQUEST", apierror.MsgChannelInvalidRequest)
			return
		}
		result, err := h.service.BeginFeishuAuth(c.Request.Context(), principal, appID, service.FeishuAuthInput{
			Domain: req.Domain,
		})
		if err != nil {
			writeChannelError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"challenge": result})
		return
	}

	// 其他渠道（如微信）走原有无请求体路径。
	result, err := h.service.BeginAuth(c.Request.Context(), principal, appID, channelType)
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
		apierror.JSON(c, http.StatusForbidden, "FORBIDDEN", apierror.MsgChannelForbidden)
	case errors.Is(err, service.ErrNotFound):
		apierror.JSON(c, http.StatusNotFound, "NOT_FOUND", apierror.MsgChannelBindingNotFound)
	case errors.Is(err, service.ErrChannelAdapterMissing):
		apierror.JSON(c, http.StatusServiceUnavailable, "CHANNEL_ADAPTER_MISSING", apierror.MsgChannelAdapterMissing)
	case errors.Is(err, service.ErrInstanceNotReady):
		// 实例重启 / 升级 / 初始化中，pod 暂不可用：映射为 409 Conflict——请求与实例当前
		// 生命周期状态冲突（与 bootstrap APP_NOT_READY、app_runtime APP_NOT_REINIT 同口径），
		// 客户端可稍候重试；不用 503 以免误指 manager 自身不可用。
		apierror.JSON(c, http.StatusConflict, "INSTANCE_NOT_READY", apierror.MsgChannelInstanceNotReady)
	default:
		apierror.JSON(c, http.StatusInternalServerError, "INTERNAL", apierror.MsgChannelUnavailable)
	}
}
