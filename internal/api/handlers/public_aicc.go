package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/service"
)

// PublicAICCHandler 处理匿名访客 AICC 公开接口。
type PublicAICCHandler struct {
	service publicAICCService
}

// publicAICCService 是公开 AICC handler 依赖的最小 service 接口。
type publicAICCService interface {
	PublicConfig(ctx context.Context, publicToken, channel string) (service.AICCPublicConfigResult, error)
	CreateSession(ctx context.Context, publicToken string, input service.AICCPublicSessionInput) (service.AICCPublicSessionResult, error)
	GetSession(ctx context.Context, sessionToken string) (service.AICCPublicSessionDetailResult, error)
	Consent(ctx context.Context, sessionToken string) error
	UploadImage(ctx context.Context, input service.AICCPublicImageInput) (service.AICCPublicImageResult, error)
	SendMessage(ctx context.Context, input service.AICCPublicMessageInput) (service.AICCPublicMessageResult, error)
	SubmitLeadValues(ctx context.Context, input service.AICCPublicLeadValuesInput) (service.AICCPublicLeadValuesResult, error)
	SubmitFeedback(ctx context.Context, input service.AICCPublicFeedbackInput) (service.AICCPublicFeedbackResult, error)
	ResolveSession(ctx context.Context, sessionToken string) (service.AICCPublicResolutionResult, error)
	UpdateSessionResolution(ctx context.Context, input service.AICCPublicResolutionInput) (service.AICCPublicResolutionResult, error)
}

// NewPublicAICCHandler 创建公开 AICC handler。
func NewPublicAICCHandler(service publicAICCService) *PublicAICCHandler {
	return &PublicAICCHandler{service: service}
}

// RegisterPublicAICCRoutes 注册匿名访客 AICC 公开路由。
func RegisterPublicAICCRoutes(router gin.IRouter, handler *PublicAICCHandler) {
	group := router.Group("/api/v1/public/aicc")
	group.GET("/agents/:publicToken/config", handler.Config)
	group.POST("/agents/:publicToken/sessions", handler.CreateSession)
	group.GET("/sessions/:sessionToken", handler.GetSession)
	group.POST("/sessions/:sessionToken/consent", handler.Consent)
	group.POST("/sessions/:sessionToken/images", handler.UploadImage)
	group.POST("/sessions/:sessionToken/messages", handler.SendMessage)
	group.POST("/sessions/:sessionToken/lead-values", handler.SubmitLeadValues)
	group.POST("/sessions/:sessionToken/resolution", handler.UpdateResolution)
	group.POST("/sessions/:sessionToken/resolve", handler.ResolveSession)
	group.POST("/sessions/:sessionToken/messages/:messageId/feedback", handler.Feedback)
}

// Config 返回访客端公开配置。
//
// @Summary      AICC 公开配置
// @Description  访客端通过公开 token 加载智能体展示配置
// @Tags         public-aicc
// @Produce      json
// @Param        publicToken  path      string  true   "公开 token"
// @Param        channel      query     string  false  "入口渠道：web_link / web_widget"
// @Success      200          {object}  map[string]service.AICCPublicConfigResult
// @Failure      404          {object}  ErrorResponse
// @Failure      500          {object}  ErrorResponse
// @Router       /public/aicc/agents/{publicToken}/config [get]
func (h *PublicAICCHandler) Config(c *gin.Context) {
	result, err := h.service.PublicConfig(c.Request.Context(), c.Param("publicToken"), c.Query("channel"))
	if err != nil {
		writePublicAICCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"config": result})
}

// CreateSession 创建访客会话。
//
// @Summary      创建 AICC 公开会话
// @Description  访客端通过公开 token 创建会话并获得 session token
// @Tags         public-aicc
// @Accept       json
// @Produce      json
// @Param        publicToken  path      string                    true  "公开 token"
// @Param        body         body      CreateAICCSessionRequest  true  "创建会话请求"
// @Success      201          {object}  map[string]service.AICCPublicSessionResult
// @Failure      400          {object}  ErrorResponse
// @Failure      403          {object}  ErrorResponse
// @Failure      429          {object}  ErrorResponse
// @Failure      404          {object}  ErrorResponse
// @Failure      500          {object}  ErrorResponse
// @Router       /public/aicc/agents/{publicToken}/sessions [post]
func (h *PublicAICCHandler) CreateSession(c *gin.Context) {
	var req CreateAICCSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	result, err := h.service.CreateSession(c.Request.Context(), c.Param("publicToken"), service.AICCPublicSessionInput{
		Channel:      req.Channel,
		SourceURL:    req.SourceURL,
		Referrer:     req.Referrer,
		SessionToken: req.SessionToken,
		Origin:       c.GetHeader("Origin"),
		RemoteIP:     c.ClientIP(),
		UserAgent:    c.GetHeader("User-Agent"),
	})
	if err != nil {
		writePublicAICCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"session": result})
}

// GetSession 返回访客当前会话消息。
//
// @Summary      AICC 公开会话详情
// @Description  访客端通过 session token 恢复当前会话消息
// @Tags         public-aicc
// @Produce      json
// @Param        sessionToken  path      string  true  "会话 token"
// @Success      200           {object}  map[string]service.AICCPublicSessionDetailResult
// @Failure      401           {object}  ErrorResponse
// @Failure      404           {object}  ErrorResponse
// @Failure      500           {object}  ErrorResponse
// @Router       /public/aicc/sessions/{sessionToken} [get]
func (h *PublicAICCHandler) GetSession(c *gin.Context) {
	result, err := h.service.GetSession(c.Request.Context(), c.Param("sessionToken"))
	if err != nil {
		writePublicAICCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"session": result})
}

// Consent 记录隐私同意。
//
// @Summary      同意 AICC 隐私说明
// @Description  访客通过 session token 记录隐私同意时间
// @Tags         public-aicc
// @Produce      json
// @Param        sessionToken  path  string  true  "会话 token"
// @Success      204
// @Failure      401  {object}  ErrorResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /public/aicc/sessions/{sessionToken}/consent [post]
func (h *PublicAICCHandler) Consent(c *gin.Context) {
	if err := h.service.Consent(c.Request.Context(), c.Param("sessionToken")); err != nil {
		writePublicAICCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// SendMessage 发送访客消息并返回助手回复。
//
// @Summary      发送 AICC 公开消息
// @Description  访客通过 session token 发送消息，manager 转发隐藏 app runtime
// @Tags         public-aicc
// @Accept       json
// @Produce      json
// @Param        sessionToken  path      string                    true  "会话 token"
// @Param        body          body      PublicAICCMessageRequest  true  "访客消息"
// @Success      200           {object}  map[string]service.AICCPublicMessageResult
// @Failure      400           {object}  ErrorResponse
// @Failure      401           {object}  ErrorResponse
// @Failure      403           {object}  ErrorResponse
// @Failure      409           {object}  ErrorResponse
// @Failure      429           {object}  ErrorResponse
// @Failure      500           {object}  ErrorResponse
// @Router       /public/aicc/sessions/{sessionToken}/messages [post]
func (h *PublicAICCHandler) SendMessage(c *gin.Context) {
	var req PublicAICCMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	result, err := h.service.SendMessage(c.Request.Context(), service.AICCPublicMessageInput{
		SessionToken: c.Param("sessionToken"),
		Text:         req.Text,
		ImageFileID:  req.ImageFileID,
	})
	if err != nil {
		writePublicAICCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": result})
}

// UploadImage 上传访客图片。
//
// @Summary      上传 AICC 公开图片
// @Description  上传访客图片并返回发送消息时引用的 image_file_id
// @Tags         public-aicc
// @Accept       application/octet-stream
// @Produce      json
// @Param        sessionToken  path      string  true  "会话 token"
// @Param        filename      query     string  true  "原始文件名"
// @Success      200           {object}  map[string]service.AICCPublicImageResult
// @Failure      400           {object}  ErrorResponse
// @Failure      401           {object}  ErrorResponse
// @Failure      404           {object}  ErrorResponse
// @Failure      413           {object}  ErrorResponse
// @Failure      429           {object}  ErrorResponse
// @Failure      503           {object}  ErrorResponse
// @Failure      500           {object}  ErrorResponse
// @Router       /public/aicc/sessions/{sessionToken}/images [post]
func (h *PublicAICCHandler) UploadImage(c *gin.Context) {
	filename := c.Query("filename")
	if filename == "" {
		apierror.JSON(c, http.StatusBadRequest, "BAD_REQUEST", apierror.MsgBadRequestGeneric)
		return
	}
	result, err := h.service.UploadImage(c.Request.Context(), service.AICCPublicImageInput{
		SessionToken: c.Param("sessionToken"),
		Filename:     filename,
		Body:         c.Request.Body,
		Size:         c.Request.ContentLength,
	})
	if err != nil {
		writePublicAICCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"image": result})
}

// SubmitLeadValues 提交留资字段。
//
// @Summary      提交 AICC 留资字段
// @Description  访客提交当前会话的留资字段，字段 key 必须来自智能体配置
// @Tags         public-aicc
// @Accept       json
// @Produce      json
// @Param        sessionToken  path      string                       true  "会话 token"
// @Param        body          body      SubmitAICCLeadValuesRequest  true  "留资字段"
// @Success      200           {object}  map[string]service.AICCPublicLeadValuesResult
// @Failure      400           {object}  ErrorResponse
// @Failure      401           {object}  ErrorResponse
// @Failure      404           {object}  ErrorResponse
// @Failure      500           {object}  ErrorResponse
// @Router       /public/aicc/sessions/{sessionToken}/lead-values [post]
func (h *PublicAICCHandler) SubmitLeadValues(c *gin.Context) {
	var req SubmitAICCLeadValuesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	result, err := h.service.SubmitLeadValues(c.Request.Context(), service.AICCPublicLeadValuesInput{
		SessionToken: c.Param("sessionToken"),
		Values:       req.Values,
	})
	if err != nil {
		writePublicAICCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"lead": result})
}

// Feedback 提交单条助手回复反馈。
//
// @Summary      提交 AICC 回复反馈
// @Description  访客反馈助手回复是否有帮助，并同步会话解决状态
// @Tags         public-aicc
// @Accept       json
// @Produce      json
// @Param        sessionToken  path      string                     true  "会话 token"
// @Param        messageId     path      string                     true  "消息 ID"
// @Param        body          body      SubmitAICCFeedbackRequest  true  "反馈内容"
// @Success      200           {object}  map[string]service.AICCPublicFeedbackResult
// @Failure      400           {object}  ErrorResponse
// @Failure      404           {object}  ErrorResponse
// @Failure      500           {object}  ErrorResponse
// @Router       /public/aicc/sessions/{sessionToken}/messages/{messageId}/feedback [post]
func (h *PublicAICCHandler) Feedback(c *gin.Context) {
	var req SubmitAICCFeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	result, err := h.service.SubmitFeedback(c.Request.Context(), service.AICCPublicFeedbackInput{
		SessionToken: c.Param("sessionToken"),
		MessageID:    c.Param("messageId"),
		Helpful:      *req.Helpful,
	})
	if err != nil {
		writePublicAICCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"feedback": result})
}

// ResolveSession 将当前公开会话标记为已解决。
//
// @Summary      标记 AICC 公开会话已解决
// @Description  访客通过 session token 将当前会话标记为已解决，不绑定单条助手回复
// @Tags         public-aicc
// @Produce      json
// @Param        sessionToken  path      string  true  "会话 token"
// @Success      200           {object}  map[string]service.AICCPublicResolutionResult
// @Failure      401           {object}  ErrorResponse
// @Failure      404           {object}  ErrorResponse
// @Failure      500           {object}  ErrorResponse
// @Router       /public/aicc/sessions/{sessionToken}/resolve [post]
func (h *PublicAICCHandler) ResolveSession(c *gin.Context) {
	result, err := h.service.ResolveSession(c.Request.Context(), c.Param("sessionToken"))
	if err != nil {
		writePublicAICCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"resolution": result})
}

// UpdateResolution 更新当前公开会话解决状态。
//
// @Summary      更新 AICC 公开会话解决状态
// @Description  访客通过 session token 将当前会话标记为已解决或未解决，不绑定单条助手回复
// @Tags         public-aicc
// @Accept       json
// @Produce      json
// @Param        sessionToken  path      string                              true  "会话 token"
// @Param        body          body      UpdateAICCSessionResolutionRequest  true  "解决状态"
// @Success      200           {object}  map[string]service.AICCPublicResolutionResult
// @Failure      400           {object}  ErrorResponse
// @Failure      401           {object}  ErrorResponse
// @Failure      404           {object}  ErrorResponse
// @Failure      500           {object}  ErrorResponse
// @Router       /public/aicc/sessions/{sessionToken}/resolution [post]
func (h *PublicAICCHandler) UpdateResolution(c *gin.Context) {
	var req UpdateAICCSessionResolutionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	result, err := h.service.UpdateSessionResolution(c.Request.Context(), service.AICCPublicResolutionInput{
		SessionToken:     c.Param("sessionToken"),
		ResolutionStatus: req.ResolutionStatus,
	})
	if err != nil {
		writePublicAICCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"resolution": result})
}

func writePublicAICCError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrAICCConsentRequired):
		apierror.JSON(c, http.StatusConflict, "AICC_CONSENT_REQUIRED", apierror.MsgAICCConsentRequired)
	case errors.Is(err, service.ErrAICCLeadRequired):
		apierror.JSON(c, http.StatusConflict, "AICC_LEAD_REQUIRED", apierror.MsgAICCLeadRequired)
	case errors.Is(err, service.ErrAICCOffline):
		apierror.JSON(c, http.StatusNotFound, "AICC_OFFLINE", apierror.MsgAICCOffline)
	case errors.Is(err, service.ErrAICCInvalidSession):
		apierror.JSON(c, http.StatusUnauthorized, "AICC_INVALID_SESSION", apierror.MsgAICCInvalidSession)
	case errors.Is(err, service.ErrAICCInvalidMessage):
		apierror.JSON(c, http.StatusNotFound, "AICC_INVALID_MESSAGE", apierror.MsgAICCInvalidMessage)
	case errors.Is(err, service.ErrAICCImageUnavailable):
		apierror.JSON(c, http.StatusServiceUnavailable, "AICC_IMAGE_UNAVAILABLE", apierror.MsgAICCImageUnavailable)
	case errors.Is(err, service.ErrAICCDomainForbidden):
		apierror.JSON(c, http.StatusForbidden, "AICC_DOMAIN_FORBIDDEN", apierror.MsgAICCDomainForbidden)
	case errors.Is(err, service.ErrAICCSensitiveWord):
		apierror.JSON(c, http.StatusBadRequest, "AICC_SENSITIVE_WORD", "消息包含暂不支持发送的内容")
	case errors.Is(err, service.ErrAICCMessageLimitExceeded):
		apierror.JSON(c, http.StatusTooManyRequests, "AICC_MESSAGE_LIMIT_EXCEEDED", "本次会话消息数量已达上限")
	case errors.Is(err, service.ErrAICCVisitorBlocked):
		apierror.JSON(c, http.StatusForbidden, "AICC_VISITOR_BLOCKED", "当前访客暂不能继续咨询")
	case errors.Is(err, service.ErrRateLimited):
		apierror.JSON(c, http.StatusTooManyRequests, "RATE_LIMITED", apierror.MsgAICCRateLimited)
	case errors.Is(err, service.ErrConversationFileTooLarge):
		apierror.JSON(c, http.StatusRequestEntityTooLarge, "CONVERSATION_FILE_TOO_LARGE", apierror.MsgConversationFileTooLarge)
	case errors.Is(err, service.ErrInvalidArgument):
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", validationServiceMessage(err, service.ErrInvalidArgument)))
	default:
		writeMappedServiceError(c, err, http.StatusInternalServerError, apierror.MsgInternal)
	}
}
