package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// customSkillService 是 handler 依赖的交付能力(包内接口,便于桩测)。
type customSkillService interface {
	Deliver(ctx context.Context, p auth.Principal, in service.DeliverCustomSkillInput) (service.CustomSkillResult, error)
	UpdateTargets(ctx context.Context, p auth.Principal, ticketID string, targets []service.CustomSkillTargetInput) error
}

// CustomSkillsHandler 暴露定制技能交付接口。
type CustomSkillsHandler struct{ service customSkillService }

// NewCustomSkillsHandler 构造 handler。
func NewCustomSkillsHandler(svc customSkillService) *CustomSkillsHandler {
	return &CustomSkillsHandler{service: svc}
}

// RegisterCustomSkillRoutes 注册交付路由(权限在 service 层判定)。
func RegisterCustomSkillRoutes(router gin.IRouter, h *CustomSkillsHandler) {
	router.POST("/api/v1/custom-skills/deliver", h.Deliver)
	router.PATCH("/api/v1/skill-tickets/:id/targets", h.UpdateTargets)
}

// Deliver 交付一个定制技能版本。
//
// @Summary  交付定制技能(平台管理员)
// @Tags     custom-skills
// @Accept   multipart/form-data
// @Produce  json
// @Security BearerAuth
// @Param    ticket_id   formData string true  "工单 id"
// @Param    description formData string false "市场展示描述"
// @Param    targets     formData string true  "目标范围 JSON 数组 [{org_id,audience}]"
// @Param    file        formData file   true  "前端打包的扁平 skill tar"
// @Success  201 {object} map[string]service.CustomSkillResult
// @Failure  400 {object} ErrorResponse
// @Failure  403 {object} ErrorResponse
// @Failure  404 {object} ErrorResponse
// @Failure  409 {object} ErrorResponse
// @Router   /custom-skills/deliver [post]
func (h *CustomSkillsHandler) Deliver(c *gin.Context) {
	// 获取 multipart file 字段,缺失时直接返回 400
	fileHeader, err := c.FormFile("file")
	if err != nil {
		apierror.JSON(c, http.StatusBadRequest, "INVALID_REQUEST", apierror.MsgCustomSkillMissingFileField)
		return
	}
	f, err := fileHeader.Open()
	if err != nil {
		apierror.JSON(c, http.StatusBadRequest, "INVALID_REQUEST", apierror.MsgCustomSkillOpenFileFailed)
		return
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		apierror.JSON(c, http.StatusBadRequest, "INVALID_REQUEST", apierror.MsgCustomSkillReadFileFailed)
		return
	}
	// targets 以 JSON 数组字符串提交,解析为 service 层目标范围;空串时留空切片,由 service 层校验"至少一个目标"。
	var targets []service.CustomSkillTargetInput
	if raw := c.PostForm("targets"); raw != "" {
		var dtos []CustomSkillTargetDTO
		if err := json.Unmarshal([]byte(raw), &dtos); err != nil {
			apierror.JSON(c, http.StatusBadRequest, "INVALID_REQUEST", apierror.MsgCustomSkillTargetsInvalidJSON)
			return
		}
		for _, d := range dtos {
			targets = append(targets, service.CustomSkillTargetInput{OrgID: d.OrgID, Audience: d.Audience})
		}
	}
	out, err := h.service.Deliver(c.Request.Context(), principalFromCtx(c), service.DeliverCustomSkillInput{
		TicketID:    c.PostForm("ticket_id"),
		Description: c.PostForm("description"),
		Data:        data,
		Targets:     targets,
	})
	if err != nil {
		writeCustomSkillError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"skill": out})
}

// UpdateTargets 编辑已交付定制技能的可见范围。
//
// @Summary  编辑已交付定制技能可见范围(平台管理员)
// @Tags     custom-skills
// @Accept   json
// @Produce  json
// @Security BearerAuth
// @Param    id   path string                            true "工单 id"
// @Param    body body UpdateCustomSkillTargetsRequest   true "目标范围"
// @Success  204
// @Failure  400 {object} ErrorResponse
// @Failure  403 {object} ErrorResponse
// @Failure  404 {object} ErrorResponse
// @Router   /skill-tickets/{id}/targets [patch]
func (h *CustomSkillsHandler) UpdateTargets(c *gin.Context) {
	var req UpdateCustomSkillTargetsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierror.JSON(c, http.StatusBadRequest, "INVALID_REQUEST", apierror.MsgCustomSkillInvalidRequest)
		return
	}
	targets := make([]service.CustomSkillTargetInput, 0, len(req.Targets))
	for _, target := range req.Targets {
		targets = append(targets, service.CustomSkillTargetInput{OrgID: target.OrgID, Audience: target.Audience})
	}
	if err := h.service.UpdateTargets(c.Request.Context(), principalFromCtx(c), c.Param("id"), targets); err != nil {
		writeCustomSkillError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// writeCustomSkillError 把交付哨兵错误映射为 HTTP 状态码 + 固定文案错误体(不回传 err.Error,避免泄露内部包装链)。
func writeCustomSkillError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrCustomSkillDenied):
		apierror.JSON(c, http.StatusForbidden, "FORBIDDEN", apierror.MsgCustomSkillDenied)
	case errors.Is(err, service.ErrSkillTicketNotFound):
		apierror.JSON(c, http.StatusNotFound, "NOT_FOUND", apierror.MsgCustomSkillTicketNotFound)
	case errors.Is(err, service.ErrCustomSkillNameMismatch):
		apierror.JSON(c, http.StatusConflict, "CONFLICT", apierror.MsgCustomSkillNameMismatch)
	case errors.Is(err, service.ErrCustomSkillInvalid):
		apierror.JSON(c, http.StatusBadRequest, "INVALID_REQUEST", apierror.MsgCustomSkillInvalidInput)
	default:
		apierror.JSON(c, http.StatusInternalServerError, "INTERNAL", apierror.MsgInternal)
	}
}
