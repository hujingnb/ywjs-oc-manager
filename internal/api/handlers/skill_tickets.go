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

// skillTicketService 是 handler 依赖的工单能力(包内接口,便于桩测)。
type skillTicketService interface {
	Submit(ctx context.Context, p auth.Principal, in service.SubmitSkillTicketInput) (service.SkillTicketResult, error)
	ListMine(ctx context.Context, p auth.Principal) ([]service.SkillTicketResult, error)
	ListAll(ctx context.Context, p auth.Principal) ([]service.SkillTicketResult, error)
	Get(ctx context.Context, p auth.Principal, id string) (service.SkillTicketDetailResult, error)
	AddComment(ctx context.Context, p auth.Principal, id, body string) (service.SkillTicketCommentResult, error)
	UpdateStatus(ctx context.Context, p auth.Principal, id, status string) error
	SetQuote(ctx context.Context, p auth.Principal, id string, cents int64) error
	Reject(ctx context.Context, p auth.Principal, id, reason string) error
	PendingBadgeCount(ctx context.Context, p auth.Principal) (int64, error)
}

// SkillTicketsHandler 暴露定制技能工单的 HTTP 接口。
type SkillTicketsHandler struct{ service skillTicketService }

// NewSkillTicketsHandler 构造 handler。
func NewSkillTicketsHandler(svc skillTicketService) *SkillTicketsHandler {
	return &SkillTicketsHandler{service: svc}
}

// RegisterSkillTicketRoutes 注册工单路由(权限在 service 层判定)。
func RegisterSkillTicketRoutes(router gin.IRouter, h *SkillTicketsHandler) {
	router.POST("/api/v1/skill-tickets", h.Submit)
	router.GET("/api/v1/skill-tickets", h.ListMine)
	router.GET("/api/v1/skill-tickets/:id", h.Get)
	router.POST("/api/v1/skill-tickets/:id/comments", h.AddComment)
	router.PATCH("/api/v1/skill-tickets/:id/status", h.UpdateStatus)
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
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "请求体格式错误"))
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

// Get 工单详情(含评论)。
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

// AddComment 追加评论(提交者在关闭态发言会重开工单)。
//
// @Summary  追加工单评论
// @Tags     skill-tickets
// @Accept   json
// @Produce  json
// @Security BearerAuth
// @Param    id   path string                       true "工单 id"
// @Param    body body AddSkillTicketCommentRequest true "评论"
// @Success  201 {object} map[string]service.SkillTicketCommentResult
// @Router   /skill-tickets/{id}/comments [post]
func (h *SkillTicketsHandler) AddComment(c *gin.Context) {
	var req AddSkillTicketCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "请求体格式错误"))
		return
	}
	out, err := h.service.AddComment(c.Request.Context(), principalFromCtx(c), c.Param("id"), req.Body)
	if err != nil {
		writeSkillTicketError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"comment": out})
}

// UpdateStatus 管理员改状态。
//
// @Summary  调整工单状态(平台管理员)
// @Tags     skill-tickets
// @Accept   json
// @Produce  json
// @Security BearerAuth
// @Param    id   path string                         true "工单 id"
// @Param    body body UpdateSkillTicketStatusRequest true "状态"
// @Success  204
// @Router   /skill-tickets/{id}/status [patch]
func (h *SkillTicketsHandler) UpdateStatus(c *gin.Context) {
	var req UpdateSkillTicketStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "请求体格式错误"))
		return
	}
	if err := h.service.UpdateStatus(c.Request.Context(), principalFromCtx(c), c.Param("id"), req.Status); err != nil {
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
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "请求体格式错误"))
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
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "请求体格式错误"))
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
		c.JSON(http.StatusForbidden, apierror.New("FORBIDDEN", "无权操作该工单"))
	case errors.Is(err, service.ErrSkillTicketNotFound):
		c.JSON(http.StatusNotFound, apierror.New("NOT_FOUND", "工单不存在"))
	case errors.Is(err, service.ErrSkillTicketInvalid):
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "工单入参非法"))
	default:
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "服务器内部错误"))
	}
}
