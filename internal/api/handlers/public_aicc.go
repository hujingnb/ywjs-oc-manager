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
	PublicConfig(ctx context.Context, publicToken string) (service.AICCPublicConfigResult, error)
	CreateSession(ctx context.Context, publicToken string, input service.AICCPublicSessionInput) (service.AICCPublicSessionResult, error)
	Consent(ctx context.Context, sessionToken string) error
	SendMessage(ctx context.Context, input service.AICCPublicMessageInput) (service.AICCPublicMessageResult, error)
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
	group.POST("/sessions/:sessionToken/consent", handler.Consent)
	group.POST("/sessions/:sessionToken/images", handler.UploadImage)
	group.POST("/sessions/:sessionToken/messages", handler.SendMessage)
	group.POST("/sessions/:sessionToken/lead-values", handler.SubmitLeadValues)
	group.POST("/messages/:messageId/feedback", handler.Feedback)
}

// Config 返回访客端公开配置。
//
// @Summary      AICC 公开配置
// @Description  访客端通过公开 token 加载智能体展示配置
// @Tags         public-aicc
// @Produce      json
// @Param        publicToken  path      string  true  "公开 token"
// @Success      200          {object}  map[string]service.AICCPublicConfigResult
// @Failure      404          {object}  ErrorResponse
// @Failure      500          {object}  ErrorResponse
// @Router       /public/aicc/agents/{publicToken}/config [get]
func (h *PublicAICCHandler) Config(c *gin.Context) {
	result, err := h.service.PublicConfig(c.Request.Context(), c.Param("publicToken"))
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
		Channel:   req.Channel,
		SourceURL: req.SourceURL,
		Referrer:  req.Referrer,
	})
	if err != nil {
		writePublicAICCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"session": result})
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
// @Failure      409           {object}  ErrorResponse
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

// UploadImage 暂保留路由契约，图片对象存储将在后续小任务接入。
//
// @Summary      上传 AICC 公开图片
// @Description  图片消息能力后续接入，当前返回 501
// @Tags         public-aicc
// @Produce      json
// @Param        sessionToken  path      string  true  "会话 token"
// @Failure      501           {object}  ErrorResponse
// @Router       /public/aicc/sessions/{sessionToken}/images [post]
func (h *PublicAICCHandler) UploadImage(c *gin.Context) {
	apierror.JSON(c, http.StatusNotImplemented, "AICC_IMAGE_NOT_IMPLEMENTED", apierror.MsgBadRequestGeneric)
}

// SubmitLeadValues 暂保留路由契约，留资提交将在后续小任务接入。
//
// @Summary      提交 AICC 留资字段
// @Description  留资写入能力后续接入，当前返回 501
// @Tags         public-aicc
// @Accept       json
// @Produce      json
// @Param        sessionToken  path      string                       true  "会话 token"
// @Param        body          body      SubmitAICCLeadValuesRequest  true  "留资字段"
// @Failure      501           {object}  ErrorResponse
// @Router       /public/aicc/sessions/{sessionToken}/lead-values [post]
func (h *PublicAICCHandler) SubmitLeadValues(c *gin.Context) {
	apierror.JSON(c, http.StatusNotImplemented, "AICC_LEAD_VALUES_NOT_IMPLEMENTED", apierror.MsgBadRequestGeneric)
}

// Feedback 暂保留路由契约，反馈写入将在后续小任务接入。
//
// @Summary      提交 AICC 回复反馈
// @Description  反馈写入能力后续接入，当前返回 501
// @Tags         public-aicc
// @Accept       json
// @Produce      json
// @Param        messageId  path      string                     true  "消息 ID"
// @Param        body       body      SubmitAICCFeedbackRequest  true  "反馈内容"
// @Failure      501        {object}  ErrorResponse
// @Router       /public/aicc/messages/{messageId}/feedback [post]
func (h *PublicAICCHandler) Feedback(c *gin.Context) {
	apierror.JSON(c, http.StatusNotImplemented, "AICC_FEEDBACK_NOT_IMPLEMENTED", apierror.MsgBadRequestGeneric)
}

func writePublicAICCError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrAICCConsentRequired):
		c.JSON(http.StatusConflict, apierror.New("AICC_CONSENT_REQUIRED", "需要先同意隐私说明"))
	case errors.Is(err, service.ErrAICCLeadRequired):
		c.JSON(http.StatusConflict, apierror.New("AICC_LEAD_REQUIRED", "需要先提交必填联系信息"))
	case errors.Is(err, service.ErrAICCOffline):
		c.JSON(http.StatusNotFound, apierror.New("AICC_OFFLINE", "客服已下线"))
	case errors.Is(err, service.ErrAICCInvalidSession):
		c.JSON(http.StatusUnauthorized, apierror.New("AICC_INVALID_SESSION", "会话已失效"))
	case errors.Is(err, service.ErrInvalidArgument):
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", validationServiceMessage(err, service.ErrInvalidArgument)))
	default:
		writeMappedServiceError(c, err, http.StatusInternalServerError, apierror.MsgInternal)
	}
}
