package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// skillTicketAttachmentService 是附件 handler 依赖的存取能力(包内接口,便于桩测)。
type skillTicketAttachmentService interface {
	Add(ctx context.Context, p auth.Principal, ticketID, fileName string, data []byte) (service.SkillTicketAttachmentResult, error)
	List(ctx context.Context, ticketID string) ([]service.SkillTicketAttachmentResult, error)
	Open(ctx context.Context, ticketID, id string) (io.ReadCloser, string, error)
}

// ticketViewer 复用工单可见性判断:附件操作前置校验调用者能否查看该工单。
// 由 SkillTicketService 满足(其 Get 内部走 CanViewSkillTicket 三层判定)。
type ticketViewer interface {
	Get(ctx context.Context, p auth.Principal, id string) (service.SkillTicketDetailResult, error)
}

// SkillTicketAttachmentsHandler 暴露工单附件上传/列表/下载。
type SkillTicketAttachmentsHandler struct {
	attachments skillTicketAttachmentService
	tickets     ticketViewer
}

// NewSkillTicketAttachmentsHandler 构造 handler(tickets 用于可见性前置校验)。
func NewSkillTicketAttachmentsHandler(a skillTicketAttachmentService, t ticketViewer) *SkillTicketAttachmentsHandler {
	return &SkillTicketAttachmentsHandler{attachments: a, tickets: t}
}

// RegisterSkillTicketAttachmentRoutes 注册附件路由(上传/列表/下载)。
func RegisterSkillTicketAttachmentRoutes(router gin.IRouter, h *SkillTicketAttachmentsHandler) {
	router.POST("/api/v1/skill-tickets/:id/attachments", h.Upload)
	router.GET("/api/v1/skill-tickets/:id/attachments", h.List)
	router.GET("/api/v1/skill-tickets/:id/attachments/:attId/download", h.Download)
}

// Upload 上传附件(multipart file);前置校验调用者能查看该工单。
//
// @Summary  上传工单附件
// @Tags     skill-tickets
// @Accept   multipart/form-data
// @Produce  json
// @Security BearerAuth
// @Param    id   path     string true "工单 id"
// @Param    file formData file   true "附件文件"
// @Success  201 {object} map[string]service.SkillTicketAttachmentResult
// @Failure  400 {object} ErrorResponse
// @Failure  403 {object} ErrorResponse
// @Failure  404 {object} ErrorResponse
// @Router   /skill-tickets/{id}/attachments [post]
func (h *SkillTicketAttachmentsHandler) Upload(c *gin.Context) {
	ticketID := c.Param("id")
	p := principalFromCtx(c)
	// 可见性前置:复用工单详情权限(看不到工单则无权上传附件)。
	if _, err := h.tickets.Get(c.Request.Context(), p, ticketID); err != nil {
		writeSkillTicketError(c, err)
		return
	}
	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "缺少 file 字段"))
		return
	}
	f, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "读取上传文件失败"))
		return
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "读取上传内容失败"))
		return
	}
	out, err := h.attachments.Add(c.Request.Context(), p, ticketID, fileHeader.Filename, data)
	if err != nil {
		writeAttachmentError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"attachment": out})
}

// List 列出工单附件元数据(可见性前置校验)。
//
// @Summary  列出工单附件
// @Tags     skill-tickets
// @Produce  json
// @Security BearerAuth
// @Param    id path string true "工单 id"
// @Success  200 {object} map[string][]service.SkillTicketAttachmentResult
// @Failure  403 {object} ErrorResponse
// @Failure  404 {object} ErrorResponse
// @Router   /skill-tickets/{id}/attachments [get]
func (h *SkillTicketAttachmentsHandler) List(c *gin.Context) {
	ticketID := c.Param("id")
	p := principalFromCtx(c)
	if _, err := h.tickets.Get(c.Request.Context(), p, ticketID); err != nil {
		writeSkillTicketError(c, err)
		return
	}
	out, err := h.attachments.List(c.Request.Context(), ticketID)
	if err != nil {
		writeAttachmentError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"attachments": out})
}

// Download 流式回传附件内容(可见性前置校验)。
//
// @Summary  下载工单附件
// @Tags     skill-tickets
// @Produce  application/octet-stream
// @Security BearerAuth
// @Param    id    path string true "工单 id"
// @Param    attId path string true "附件 id"
// @Success  200 {file} binary
// @Failure  403 {object} ErrorResponse
// @Failure  404 {object} ErrorResponse
// @Router   /skill-tickets/{id}/attachments/{attId}/download [get]
func (h *SkillTicketAttachmentsHandler) Download(c *gin.Context) {
	p := principalFromCtx(c)
	if _, err := h.tickets.Get(c.Request.Context(), p, c.Param("id")); err != nil {
		writeSkillTicketError(c, err)
		return
	}
	rc, fileName, err := h.attachments.Open(c.Request.Context(), c.Param("id"), c.Param("attId"))
	if err != nil {
		writeAttachmentError(c, err)
		return
	}
	defer func() { _ = rc.Close() }()
	// 用 RFC 5987 的 filename* 写 UTF-8 文件名,兼容含中文/空格的附件名。
	c.Header("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(fileName))
	c.Header("Content-Type", "application/octet-stream")
	_, _ = io.Copy(c.Writer, rc)
}

// writeAttachmentError 把附件哨兵错误映射为 HTTP 状态码 + 固定文案错误体(不回传 err.Error,避免泄露内部包装链)。
func writeAttachmentError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrSkillTicketAttachmentNotFound):
		c.JSON(http.StatusNotFound, apierror.New("NOT_FOUND", "附件不存在"))
	case errors.Is(err, service.ErrSkillTicketAttachmentInvalid):
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "附件入参非法"))
	default:
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "服务器内部错误"))
	}
}
