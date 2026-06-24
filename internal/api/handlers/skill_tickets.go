package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// skillTicketService 是 handler 依赖的工单能力(包内接口,便于桩测)。
type skillTicketService interface {
	Submit(ctx context.Context, p auth.Principal, in service.SubmitSkillTicketInput) (service.SkillTicketResult, error)
	ListMine(ctx context.Context, p auth.Principal) ([]service.SkillTicketResult, error)
	ListAll(ctx context.Context, p auth.Principal) ([]service.SkillTicketResult, error)
	Get(ctx context.Context, p auth.Principal, id string) (service.SkillTicketDetailResult, error)
	StartProcessing(ctx context.Context, p auth.Principal, id string) error
	ReopenRejected(ctx context.Context, p auth.Principal, id string) error
	SetQuote(ctx context.Context, p auth.Principal, id string, cents int64) error
	Reject(ctx context.Context, p auth.Principal, id, reason string) error
	PendingBadgeCount(ctx context.Context, p auth.Principal) (int64, error)
}

// skillTicketMessageService 是 handler 依赖的统一消息能力(text/image/file)。
type skillTicketMessageService interface {
	SendText(ctx context.Context, p auth.Principal, ticketID, text string) (service.SkillTicketMessageResult, error)
	SendFile(ctx context.Context, p auth.Principal, ticketID, fileName, contentType string, data []byte) (service.SkillTicketMessageResult, error)
	DownloadFile(ctx context.Context, p auth.Principal, ticketID, messageID string) ([]byte, string, string, error)
}

// SkillTicketsHandler 暴露定制技能工单的 HTTP 接口。
type SkillTicketsHandler struct {
	service  skillTicketService
	messages skillTicketMessageService
}

// NewSkillTicketsHandler 构造 handler。
func NewSkillTicketsHandler(svc skillTicketService, messages skillTicketMessageService) *SkillTicketsHandler {
	return &SkillTicketsHandler{service: svc, messages: messages}
}

// RegisterSkillTicketRoutes 注册工单路由(权限在 service 层判定)。
func RegisterSkillTicketRoutes(router gin.IRouter, h *SkillTicketsHandler) {
	router.POST("/api/v1/skill-tickets", h.Submit)
	router.GET("/api/v1/skill-tickets", h.ListMine)
	router.GET("/api/v1/skill-tickets/:id", h.Get)
	router.POST("/api/v1/skill-tickets/:id/messages", h.SendMessage)
	router.POST("/api/v1/skill-tickets/:id/messages/upload", h.UploadMessage)
	router.GET("/api/v1/skill-tickets/:id/messages/:msgId/download", h.DownloadMessage)
	router.POST("/api/v1/skill-tickets/:id/start", h.StartProcessing)
	router.POST("/api/v1/skill-tickets/:id/reopen", h.ReopenRejected)
	router.PATCH("/api/v1/skill-tickets/:id/quote", h.SetQuote)
	router.POST("/api/v1/skill-tickets/:id/reject", h.Reject)
	router.GET("/api/v1/admin/skill-tickets", h.ListAll)
	router.GET("/api/v1/admin/skill-tickets/badge", h.Badge)
}

// Submit 提交需求工单。
//
// @Summary  提交定制技能需求工单
// @Tags     skill-tickets
// @Accept   json
// @Produce  json
// @Security BearerAuth
// @Param    body body SubmitSkillTicketRequest true "需求"
// @Success  201 {object} map[string]service.SkillTicketResult
// @Failure  400 {object} ErrorResponse
// @Router   /skill-tickets [post]
func (h *SkillTicketsHandler) Submit(c *gin.Context) {
	var req SubmitSkillTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierror.JSON(c, http.StatusBadRequest, "INVALID_REQUEST", apierror.MsgSkillTicketInvalidRequest)
		return
	}
	out, err := h.service.Submit(c.Request.Context(), principalFromCtx(c), service.SubmitSkillTicketInput{
		Title: req.Title, Description: req.Description,
	})
	if err != nil {
		writeSkillTicketError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"ticket": out})
}

// ListMine 列出我提交的工单。
//
// @Summary  列出我的定制技能工单
// @Tags     skill-tickets
// @Produce  json
// @Security BearerAuth
// @Success  200 {object} map[string][]service.SkillTicketResult
// @Router   /skill-tickets [get]
func (h *SkillTicketsHandler) ListMine(c *gin.Context) {
	out, err := h.service.ListMine(c.Request.Context(), principalFromCtx(c))
	if err != nil {
		writeSkillTicketError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"tickets": out})
}

// ListAll 平台管理员工单队列。
//
// @Summary  定制技能工单队列(平台管理员)
// @Tags     skill-tickets
// @Produce  json
// @Security BearerAuth
// @Success  200 {object} map[string][]service.SkillTicketResult
// @Router   /admin/skill-tickets [get]
func (h *SkillTicketsHandler) ListAll(c *gin.Context) {
	out, err := h.service.ListAll(c.Request.Context(), principalFromCtx(c))
	if err != nil {
		writeSkillTicketError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"tickets": out})
}

// Get 工单详情(含消息流)。
//
// @Summary  定制技能工单详情
// @Tags     skill-tickets
// @Produce  json
// @Security BearerAuth
// @Param    id path string true "工单 id"
// @Success  200 {object} map[string]service.SkillTicketDetailResult
// @Failure  404 {object} ErrorResponse
// @Router   /skill-tickets/{id} [get]
func (h *SkillTicketsHandler) Get(c *gin.Context) {
	out, err := h.service.Get(c.Request.Context(), principalFromCtx(c), c.Param("id"))
	if err != nil {
		writeSkillTicketError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ticket": out})
}

// SendMessage 发送文本消息(提交者在关闭态发言会由 service 自动重开工单)。
//
// @Summary  发送工单文本消息
// @Tags     skill-tickets
// @Accept   json
// @Produce  json
// @Security BearerAuth
// @Param    id   path string                         true "工单 id"
// @Param    body body SendSkillTicketMessageRequest  true "消息"
// @Success  201 {object} map[string]service.SkillTicketMessageResult
// @Router   /skill-tickets/{id}/messages [post]
func (h *SkillTicketsHandler) SendMessage(c *gin.Context) {
	var req SendSkillTicketMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierror.JSON(c, http.StatusBadRequest, "INVALID_REQUEST", apierror.MsgSkillTicketInvalidRequest)
		return
	}
	out, err := h.messages.SendText(c.Request.Context(), principalFromCtx(c), c.Param("id"), req.Text)
	if err != nil {
		writeSkillTicketError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"message": out})
}

// UploadMessage 上传图片/文件消息,每个文件自身就是一条消息。
//
// @Summary  上传工单图片/文件消息
// @Tags     skill-tickets
// @Accept   multipart/form-data
// @Produce  json
// @Security BearerAuth
// @Param    id   path     string true "工单 id"
// @Param    file formData file   true "图片或文件"
// @Success  201 {object} map[string]service.SkillTicketMessageResult
// @Router   /skill-tickets/{id}/messages/upload [post]
func (h *SkillTicketsHandler) UploadMessage(c *gin.Context) {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		apierror.JSON(c, http.StatusBadRequest, "INVALID_REQUEST", apierror.MsgSkillTicketMissingFileField)
		return
	}
	f, err := fileHeader.Open()
	if err != nil {
		apierror.JSON(c, http.StatusBadRequest, "INVALID_REQUEST", apierror.MsgSkillTicketOpenFileFailed)
		return
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		apierror.JSON(c, http.StatusBadRequest, "INVALID_REQUEST", apierror.MsgSkillTicketReadFileFailed)
		return
	}
	out, err := h.messages.SendFile(
		c.Request.Context(),
		principalFromCtx(c),
		c.Param("id"),
		filepath.Base(fileHeader.Filename),
		fileHeader.Header.Get("Content-Type"),
		data,
	)
	if err != nil {
		writeSkillTicketError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"message": out})
}

// DownloadMessage 下载图片/文件消息内容;text 消息由 service 返回 Invalid。
//
// @Summary  下载工单文件消息
// @Tags     skill-tickets
// @Produce  application/octet-stream
// @Security BearerAuth
// @Param    id    path string true "工单 id"
// @Param    msgId path string true "消息 id"
// @Success  200 {file} binary
// @Router   /skill-tickets/{id}/messages/{msgId}/download [get]
func (h *SkillTicketsHandler) DownloadMessage(c *gin.Context) {
	data, fileName, contentType, err := h.messages.DownloadFile(c.Request.Context(), principalFromCtx(c), c.Param("id"), c.Param("msgId"))
	if err != nil {
		writeSkillTicketError(c, err)
		return
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	// UTF-8 文件名走 filename*,避免中文/空格在不同浏览器中乱码。
	c.Header("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(fileName))
	c.Data(http.StatusOK, contentType, data)
}

// StartProcessing 管理员开始制作工单。
//
// @Summary  开始制作工单(平台管理员)
// @Tags     skill-tickets
// @Security BearerAuth
// @Param    id path string true "工单 id"
// @Success  204
// @Router   /skill-tickets/{id}/start [post]
func (h *SkillTicketsHandler) StartProcessing(c *gin.Context) {
	if err := h.service.StartProcessing(c.Request.Context(), principalFromCtx(c), c.Param("id")); err != nil {
		writeSkillTicketError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ReopenRejected 管理员重新受理已拒绝工单。
//
// @Summary  重新受理已拒绝工单(平台管理员)
// @Tags     skill-tickets
// @Security BearerAuth
// @Param    id path string true "工单 id"
// @Success  204
// @Router   /skill-tickets/{id}/reopen [post]
func (h *SkillTicketsHandler) ReopenRejected(c *gin.Context) {
	if err := h.service.ReopenRejected(c.Request.Context(), principalFromCtx(c), c.Param("id")); err != nil {
		writeSkillTicketError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// SetQuote 管理员报价。
//
// @Summary  设置工单报价(平台管理员)
// @Tags     skill-tickets
// @Accept   json
// @Produce  json
// @Security BearerAuth
// @Param    id   path string                      true "工单 id"
// @Param    body body SetSkillTicketQuoteRequest  true "报价(分)"
// @Success  204
// @Router   /skill-tickets/{id}/quote [patch]
func (h *SkillTicketsHandler) SetQuote(c *gin.Context) {
	var req SetSkillTicketQuoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierror.JSON(c, http.StatusBadRequest, "INVALID_REQUEST", apierror.MsgSkillTicketInvalidRequest)
		return
	}
	if err := h.service.SetQuote(c.Request.Context(), principalFromCtx(c), c.Param("id"), req.QuoteAmountCents); err != nil {
		writeSkillTicketError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Reject 管理员拒绝。
//
// @Summary  拒绝工单(平台管理员)
// @Tags     skill-tickets
// @Accept   json
// @Produce  json
// @Security BearerAuth
// @Param    id   path string                   true "工单 id"
// @Param    body body RejectSkillTicketRequest true "拒绝原因"
// @Success  204
// @Router   /skill-tickets/{id}/reject [post]
func (h *SkillTicketsHandler) Reject(c *gin.Context) {
	var req RejectSkillTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierror.JSON(c, http.StatusBadRequest, "INVALID_REQUEST", apierror.MsgSkillTicketInvalidRequest)
		return
	}
	if err := h.service.Reject(c.Request.Context(), principalFromCtx(c), c.Param("id"), req.Reason); err != nil {
		writeSkillTicketError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Badge 待处理工单角标(平台管理员)。
//
// @Summary  待处理工单数角标
// @Tags     skill-tickets
// @Produce  json
// @Security BearerAuth
// @Success  200 {object} map[string]int64
// @Router   /admin/skill-tickets/badge [get]
func (h *SkillTicketsHandler) Badge(c *gin.Context) {
	n, err := h.service.PendingBadgeCount(c.Request.Context(), principalFromCtx(c))
	if err != nil {
		writeSkillTicketError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"pending": n})
}

// writeSkillTicketError 把工单哨兵错误映射为 HTTP 状态码。
func writeSkillTicketError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrSkillTicketDenied):
		apierror.JSON(c, http.StatusForbidden, "FORBIDDEN", apierror.MsgSkillTicketForbidden)
	case errors.Is(err, service.ErrSkillTicketNotFound):
		apierror.JSON(c, http.StatusNotFound, "NOT_FOUND", apierror.MsgSkillTicketNotFound)
	case errors.Is(err, service.ErrSkillTicketInvalid):
		apierror.JSON(c, http.StatusBadRequest, "INVALID_REQUEST", apierror.MsgSkillTicketInvalidInput)
	default:
		apierror.JSON(c, http.StatusInternalServerError, "INTERNAL", apierror.MsgInternal)
	}
}
