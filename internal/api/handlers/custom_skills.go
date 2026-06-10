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
	// targets 以 JSON 数组字符串提交,解析为 service 层目标范围;空串时留空切片,由 service 层校验"至少一个目标"。
	var targets []service.CustomSkillTargetInput
	if raw := c.PostForm("targets"); raw != "" {
		var dtos []CustomSkillTargetDTO
		if err := json.Unmarshal([]byte(raw), &dtos); err != nil {
			c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "targets 不是合法 JSON"))
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

// writeCustomSkillError 把交付哨兵错误映射为 HTTP 状态码 + 固定文案错误体(不回传 err.Error,避免泄露内部包装链)。
func writeCustomSkillError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrCustomSkillDenied):
		c.JSON(http.StatusForbidden, apierror.New("FORBIDDEN", "无权交付定制技能"))
	case errors.Is(err, service.ErrSkillTicketNotFound):
		c.JSON(http.StatusNotFound, apierror.New("NOT_FOUND", "工单不存在"))
	case errors.Is(err, service.ErrCustomSkillNameMismatch):
		c.JSON(http.StatusConflict, apierror.New("CONFLICT", "迭代交付必须沿用同一技能名"))
	case errors.Is(err, service.ErrCustomSkillInvalid):
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "交付入参非法"))
	default:
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "服务器内部错误"))
	}
}
