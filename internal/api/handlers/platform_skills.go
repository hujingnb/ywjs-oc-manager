package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// platformSkillService 是 handler 依赖的平台库能力（包内接口，便于测试桩）。
type platformSkillService interface {
	List(ctx context.Context, principal auth.Principal) ([]service.PlatformSkillResult, error)
	Upload(ctx context.Context, principal auth.Principal, in service.PlatformSkillUploadInput) (service.PlatformSkillResult, error)
	Delete(ctx context.Context, principal auth.Principal, id string) error
}

// PlatformSkillsHandler 暴露平台库 skill 的 HTTP 接口。
type PlatformSkillsHandler struct{ service platformSkillService }

// NewPlatformSkillsHandler 构造 handler。
func NewPlatformSkillsHandler(svc platformSkillService) *PlatformSkillsHandler {
	return &PlatformSkillsHandler{service: svc}
}

// RegisterPlatformSkillRoutes 注册平台库路由（权限在 service 层判定）。
func RegisterPlatformSkillRoutes(router gin.IRouter, h *PlatformSkillsHandler) {
	router.GET("/api/v1/platform-skills", h.List)
	router.POST("/api/v1/platform-skills", h.Upload)
	router.DELETE("/api/v1/platform-skills/:id", h.Delete)
}

// List 列出平台库 skill。
//
// @Summary  列出平台库 skill
// @Tags     platform-skills
// @Produce  json
// @Security BearerAuth
// @Success  200 {object} map[string][]service.PlatformSkillResult
// @Failure  403 {object} ErrorResponse
// @Router   /platform-skills [get]
func (h *PlatformSkillsHandler) List(c *gin.Context) {
	out, err := h.service.List(c.Request.Context(), principalFromCtx(c))
	if err != nil {
		writePlatformSkillError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"skills": out})
}

// Upload 上传平台库 skill（multipart：name/version/description + file）。
//
// @Summary  上传平台库 skill
// @Tags     platform-skills
// @Accept   multipart/form-data
// @Produce  json
// @Security BearerAuth
// @Param    name        formData string true  "skill 名"
// @Param    version     formData string true  "版本"
// @Param    description formData string false "描述"
// @Param    file        formData file   true  "skill tar 归档"
// @Success  201 {object} map[string]service.PlatformSkillResult
// @Failure  400 {object} ErrorResponse
// @Failure  403 {object} ErrorResponse
// @Failure  409 {object} ErrorResponse
// @Router   /platform-skills [post]
func (h *PlatformSkillsHandler) Upload(c *gin.Context) {
	// 获取 multipart file 字段，缺失时直接返回 400
	fileHeader, err := c.FormFile("file")
	if err != nil {
		apierror.JSON(c, http.StatusBadRequest, "INVALID_REQUEST", apierror.MsgPlatformSkillMissingFileField)
		return
	}
	f, err := fileHeader.Open()
	if err != nil {
		apierror.JSON(c, http.StatusBadRequest, "INVALID_REQUEST", apierror.MsgPlatformSkillOpenFileFailed)
		return
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		apierror.JSON(c, http.StatusBadRequest, "INVALID_REQUEST", apierror.MsgPlatformSkillReadFileFailed)
		return
	}
	out, err := h.service.Upload(c.Request.Context(), principalFromCtx(c), service.PlatformSkillUploadInput{
		Name:        c.PostForm("name"),
		Version:     c.PostForm("version"),
		Description: c.PostForm("description"),
		Data:        data,
	})
	if err != nil {
		writePlatformSkillError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"skill": out})
}

// Delete 删除平台库 skill。
//
// @Summary  删除平台库 skill
// @Tags     platform-skills
// @Produce  json
// @Security BearerAuth
// @Param    id path string true "skill id"
// @Success  204
// @Failure  403 {object} ErrorResponse
// @Failure  404 {object} ErrorResponse
// @Router   /platform-skills/{id} [delete]
func (h *PlatformSkillsHandler) Delete(c *gin.Context) {
	if err := h.service.Delete(c.Request.Context(), principalFromCtx(c), c.Param("id")); err != nil {
		writePlatformSkillError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// writePlatformSkillError 把平台库哨兵错误映射为 HTTP 状态码 + 固定文案错误体（不回传 err.Error，避免泄露内部包装链）。
func writePlatformSkillError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrPlatformSkillDenied):
		apierror.JSON(c, http.StatusForbidden, "FORBIDDEN", apierror.MsgPlatformSkillDenied)
	case errors.Is(err, service.ErrPlatformSkillNotFound):
		apierror.JSON(c, http.StatusNotFound, "NOT_FOUND", apierror.MsgPlatformSkillNotFound)
	case errors.Is(err, service.ErrPlatformSkillNameVersionTaken):
		apierror.JSON(c, http.StatusConflict, "CONFLICT", apierror.MsgPlatformSkillNameVersionTaken)
	case errors.Is(err, service.ErrPlatformSkillInvalid):
		apierror.JSON(c, http.StatusBadRequest, "INVALID_REQUEST", apierror.MsgPlatformSkillInvalidInput)
	default:
		apierror.JSON(c, http.StatusInternalServerError, "INTERNAL", apierror.MsgInternal)
	}
}
