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

// assistantVersionService 是 handler 依赖的版本 service 能力集合。
type assistantVersionService interface {
	List(ctx context.Context, principal auth.Principal) ([]service.AssistantVersionResult, error)
	Get(ctx context.Context, principal auth.Principal, id string) (service.AssistantVersionResult, error)
	Create(ctx context.Context, principal auth.Principal, in service.AssistantVersionInput) (service.AssistantVersionResult, error)
	Update(ctx context.Context, principal auth.Principal, id string, in service.AssistantVersionInput) (service.AssistantVersionResult, error)
	Delete(ctx context.Context, principal auth.Principal, id string) error
	AddSkillFromLibrary(ctx context.Context, principal auth.Principal, id string, in service.AddSkillFromLibraryInput) (service.AssistantVersionResult, error)
	DeleteSkill(ctx context.Context, principal auth.Principal, id, skillName string) (service.AssistantVersionResult, error)
	ListRuntimeImages(ctx context.Context, principal auth.Principal) ([]service.RuntimeImageOption, error)
}

// AssistantVersionsHandler 暴露助手版本目录的 HTTP 接口。
type AssistantVersionsHandler struct {
	service assistantVersionService
}

// NewAssistantVersionsHandler 创建版本 handler。
func NewAssistantVersionsHandler(svc assistantVersionService) *AssistantVersionsHandler {
	return &AssistantVersionsHandler{service: svc}
}

// RegisterAssistantVersionRoutes 注册助手版本与镜像列表路由。
func RegisterAssistantVersionRoutes(router gin.IRouter, h *AssistantVersionsHandler) {
	router.GET("/api/v1/assistant-versions", h.List)
	router.POST("/api/v1/assistant-versions", h.Create)
	router.GET("/api/v1/assistant-versions/:id", h.Get)
	router.PUT("/api/v1/assistant-versions/:id", h.Update)
	router.DELETE("/api/v1/assistant-versions/:id", h.Delete)
	router.POST("/api/v1/assistant-versions/:id/skills", h.AddSkillFromLibrary)
	router.DELETE("/api/v1/assistant-versions/:id/skills/:skill", h.DeleteSkill)
	router.GET("/api/v1/runtime-images", h.ListRuntimeImages)
}

// writeAVError 把 service 哨兵错误映射成 HTTP 响应。
func writeAVError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrAssistantVersionDenied):
		apierror.JSON(c, http.StatusForbidden, "FORBIDDEN", apierror.MsgAssistantVersionForbidden)
	case errors.Is(err, service.ErrAssistantVersionNotFound):
		apierror.JSON(c, http.StatusNotFound, "ASSISTANT_VERSION_NOT_FOUND", apierror.MsgAssistantVersionNotFound)
	case errors.Is(err, service.ErrAssistantVersionNameTaken):
		apierror.JSON(c, http.StatusConflict, "ASSISTANT_VERSION_NAME_TAKEN", apierror.MsgAssistantVersionNameTaken)
	case errors.Is(err, service.ErrAssistantVersionInUse):
		apierror.JSON(c, http.StatusConflict, "ASSISTANT_VERSION_IN_USE", apierror.MsgAssistantVersionInUse)
	case errors.Is(err, service.ErrAssistantVersionSkillNameTaken):
		apierror.JSON(c, http.StatusConflict, "ASSISTANT_VERSION_SKILL_NAME_TAKEN", apierror.MsgAssistantVersionSkillNameTaken)
	case errors.Is(err, service.ErrPlatformSkillNotFound):
		apierror.JSON(c, http.StatusNotFound, "PLATFORM_SKILL_NOT_FOUND", apierror.MsgAssistantVersionPlatformSkillNotFound)
	case errors.Is(err, service.ErrIndustryKnowledgeNotFound):
		// 「行业知识库不存在」中文与 industry_knowledge domain 完全一致，跨 domain 复用同一 key。
		apierror.JSON(c, http.StatusNotFound, "INDUSTRY_KNOWLEDGE_NOT_FOUND", apierror.MsgIndustryKnowledgeNotFound)
	case errors.Is(err, service.ErrAppSkillSourceUnknown):
		apierror.JSON(c, http.StatusBadRequest, "APP_SKILL_SOURCE_UNKNOWN", apierror.MsgAssistantVersionSkillSourceUnknown)
	case errors.Is(err, service.ErrAssistantVersionInvalid):
		// 该分支文案来自 service 校验链动态原因（err.Error()），无静态可译部分，保留原样。
		c.JSON(http.StatusBadRequest, apierror.New("ASSISTANT_VERSION_INVALID", err.Error()))
	case errors.Is(err, service.ErrSkillMarketUpstreamUnavailable):
		// 上游技能市场（clawhub）下载失败且缓存未命中 → 502，区别于 manager 自身 500。
		apierror.JSON(c, http.StatusBadGateway, "UPSTREAM_UNAVAILABLE", apierror.MsgAssistantVersionUpstreamUnavailable)
	default:
		apierror.JSON(c, http.StatusInternalServerError, "INTERNAL", apierror.MsgAssistantVersionInternal)
	}
}

// routingToMap 把 8 槽位 DTO 转成 service 需要的 map（空值槽位也带上，service 内归一化）。
func routingToMap(d AssistantVersionRoutingDTO) map[string]string {
	return map[string]string{
		"vision": d.Vision, "compression": d.Compression, "web_extract": d.WebExtract,
		"session_search": d.SessionSearch, "title_generation": d.TitleGeneration,
		"approval": d.Approval, "skills_hub": d.SkillsHub, "mcp": d.Mcp,
	}
}

// List 返回全部助手版本。
//
// @Summary  助手版本列表
// @Tags     assistant-versions
// @Produce  json
// @Security BearerAuth
// @Success  200 {object} map[string][]service.AssistantVersionResult
// @Failure  403 {object} ErrorResponse
// @Router   /assistant-versions [get]
func (h *AssistantVersionsHandler) List(c *gin.Context) {
	out, err := h.service.List(c.Request.Context(), principalFromCtx(c))
	if err != nil {
		writeAVError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"versions": out})
}

// Get 返回单个助手版本。
//
// @Summary  助手版本详情
// @Tags     assistant-versions
// @Produce  json
// @Security BearerAuth
// @Param    id path string true "版本 ID"
// @Success  200 {object} map[string]service.AssistantVersionResult
// @Failure  403 {object} ErrorResponse
// @Failure  404 {object} ErrorResponse
// @Router   /assistant-versions/{id} [get]
func (h *AssistantVersionsHandler) Get(c *gin.Context) {
	out, err := h.service.Get(c.Request.Context(), principalFromCtx(c), c.Param("id"))
	if err != nil {
		writeAVError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"version": out})
}

// Create 创建助手版本。
//
// @Summary  创建助手版本
// @Tags     assistant-versions
// @Accept   json
// @Produce  json
// @Security BearerAuth
// @Param    body body CreateAssistantVersionRequest true "版本"
// @Success  201 {object} map[string]service.AssistantVersionResult
// @Failure  400 {object} ErrorResponse
// @Failure  403 {object} ErrorResponse
// @Failure  404 {object} ErrorResponse
// @Router   /assistant-versions [post]
func (h *AssistantVersionsHandler) Create(c *gin.Context) {
	var req CreateAssistantVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierror.JSON(c, http.StatusBadRequest, "INVALID_REQUEST", apierror.MsgAssistantVersionInvalidRequest)
		return
	}
	out, err := h.service.Create(c.Request.Context(), principalFromCtx(c), service.AssistantVersionInput{
		Name: req.Name, Description: req.Description, SystemPrompt: req.SystemPrompt,
		ImageID: req.ImageID, MainModel: req.MainModel, Routing: routingToMap(req.Routing),
		IndustryKnowledgeBaseIDs:      req.IndustryKnowledgeBaseIDs,
		ReplaceIndustryKnowledgeBases: true,
	})
	if err != nil {
		writeAVError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"version": out})
}

// Update 编辑助手版本。
//
// @Summary  编辑助手版本
// @Tags     assistant-versions
// @Accept   json
// @Produce  json
// @Security BearerAuth
// @Param    id   path string                        true "版本 ID"
// @Param    body body UpdateAssistantVersionRequest true "版本"
// @Success  200 {object} map[string]service.AssistantVersionResult
// @Failure  400 {object} ErrorResponse
// @Failure  403 {object} ErrorResponse
// @Failure  404 {object} ErrorResponse
// @Router   /assistant-versions/{id} [put]
func (h *AssistantVersionsHandler) Update(c *gin.Context) {
	var req UpdateAssistantVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierror.JSON(c, http.StatusBadRequest, "INVALID_REQUEST", apierror.MsgAssistantVersionInvalidRequest)
		return
	}
	input := service.AssistantVersionInput{
		Name: req.Name, Description: req.Description, SystemPrompt: req.SystemPrompt,
		ImageID: req.ImageID, MainModel: req.MainModel, Routing: routingToMap(req.Routing),
	}
	if req.IndustryKnowledgeBaseIDs != nil {
		input.IndustryKnowledgeBaseIDs = *req.IndustryKnowledgeBaseIDs
		input.ReplaceIndustryKnowledgeBases = true
	}
	out, err := h.service.Update(c.Request.Context(), principalFromCtx(c), c.Param("id"), input)
	if err != nil {
		writeAVError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"version": out})
}

// Delete 删除助手版本。
//
// @Summary  删除助手版本
// @Tags     assistant-versions
// @Produce  json
// @Security BearerAuth
// @Param    id path string true "版本 ID"
// @Success  204 "已删除"
// @Failure  403 {object} ErrorResponse
// @Failure  404 {object} ErrorResponse
// @Failure  409 {object} ErrorResponse
// @Router   /assistant-versions/{id} [delete]
func (h *AssistantVersionsHandler) Delete(c *gin.Context) {
	if err := h.service.Delete(c.Request.Context(), principalFromCtx(c), c.Param("id")); err != nil {
		writeAVError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// AddSkillFromLibrary 从平台库选一个 skill 配进版本。
//
// @Summary  从库选 skill 配进版本
// @Tags     assistant-versions
// @Accept   json
// @Produce  json
// @Security BearerAuth
// @Param    id   path string                         true "版本 ID"
// @Param    body body AddSkillFromLibraryRequest     true "库选 skill 入参"
// @Success  200 {object} map[string]service.AssistantVersionResult
// @Failure  400 {object} ErrorResponse
// @Failure  403 {object} ErrorResponse
// @Failure  404 {object} ErrorResponse
// @Failure  409 {object} ErrorResponse
// @Router   /assistant-versions/{id}/skills [post]
func (h *AssistantVersionsHandler) AddSkillFromLibrary(c *gin.Context) {
	var req AddSkillFromLibraryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierror.JSON(c, http.StatusBadRequest, "INVALID_REQUEST", apierror.MsgAssistantVersionInvalidRequest)
		return
	}
	out, err := h.service.AddSkillFromLibrary(c.Request.Context(), principalFromCtx(c), c.Param("id"), service.AddSkillFromLibraryInput{
		Source: req.Source, SourceRef: req.SourceRef, Name: req.Name, Version: req.Version,
	})
	if err != nil {
		writeAVError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"version": out})
}

// DeleteSkill 删除版本下的一个 skill。
//
// @Summary  删除版本 skill
// @Tags     assistant-versions
// @Produce  json
// @Security BearerAuth
// @Param    id    path string true "版本 ID"
// @Param    skill path string true "skill 名称"
// @Success  200 {object} map[string]service.AssistantVersionResult
// @Failure  400 {object} ErrorResponse
// @Failure  403 {object} ErrorResponse
// @Router   /assistant-versions/{id}/skills/{skill} [delete]
func (h *AssistantVersionsHandler) DeleteSkill(c *gin.Context) {
	out, err := h.service.DeleteSkill(c.Request.Context(), principalFromCtx(c), c.Param("id"), c.Param("skill"))
	if err != nil {
		writeAVError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"version": out})
}

// ListRuntimeImages 返回配置文件中的可选 Hermes 镜像。
//
// @Summary  可选 Hermes 镜像列表
// @Tags     assistant-versions
// @Produce  json
// @Security BearerAuth
// @Success  200 {object} map[string][]service.RuntimeImageOption
// @Failure  403 {object} ErrorResponse
// @Router   /runtime-images [get]
func (h *AssistantVersionsHandler) ListRuntimeImages(c *gin.Context) {
	out, err := h.service.ListRuntimeImages(c.Request.Context(), principalFromCtx(c))
	if err != nil {
		writeAVError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"images": out})
}
