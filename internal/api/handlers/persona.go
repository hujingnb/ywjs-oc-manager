package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

type personaService interface {
	GetCurrent(ctx context.Context, principal auth.Principal, orgID string) (service.PersonaResult, error)
	Replace(ctx context.Context, principal auth.Principal, orgID string, input service.PersonaInput) (service.PersonaResult, error)
}

// PersonaHandler 暴露组织 AI 人设的读写接口。
type PersonaHandler struct {
	service personaService
	tokens  *auth.TokenManager
}

// NewPersonaHandler 创建 handler。
func NewPersonaHandler(svc personaService, tokens *auth.TokenManager) *PersonaHandler {
	return &PersonaHandler{service: svc, tokens: tokens}
}

// RegisterPersonaRoutes 注册路由。
func RegisterPersonaRoutes(router gin.IRouter, handler *PersonaHandler) {
	router.GET("/api/v1/orgs/:orgId/persona", handler.Get)
	router.PUT("/api/v1/orgs/:orgId/persona", handler.Put)
}

// Get 返回组织当前生效人设。
//
// @Summary      获取组织人设
// @Description  返回指定组织当前生效的 AI 人设配置
// @Tags         persona
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path      string  true  "组织 ID"
// @Success      200    {object}  map[string]service.PersonaResult
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /orgs/{orgId}/persona [get]
func (h *PersonaHandler) Get(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	result, err := h.service.GetCurrent(c.Request.Context(), principal, c.Param("orgId"))
	if err != nil {
		writePersonaError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"persona": result})
}

// Put 写入新版本人设。
//
// @Summary      写入组织人设
// @Description  替换指定组织的 AI 人设配置，写入后立即生效
// @Tags         persona
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path      string         true  "组织 ID"
// @Param        body   body      PersonaRequest true  "人设请求"
// @Success      200    {object}  map[string]service.PersonaResult
// @Failure      400    {object}  ErrorResponse
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /orgs/{orgId}/persona [put]
func (h *PersonaHandler) Put(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	var req PersonaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	result, err := h.service.Replace(c.Request.Context(), principal, c.Param("orgId"), service.PersonaInput{
		SystemPrompt:        req.SystemPrompt,
		ConversationRules:   req.ConversationRules,
		ForbiddenRules:      req.ForbiddenRules,
		ReplyStyle:          req.ReplyStyle,
		AllowMemberOverride: req.AllowMemberOverride,
	})
	if err != nil {
		writePersonaError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"persona": result})
}

func (h *PersonaHandler) principal(c *gin.Context) (auth.Principal, bool) {
	token, ok := bearerToken(c.GetHeader("Authorization"))
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "缺少访问令牌"})
		return auth.Principal{}, false
	}
	principal, err := h.tokens.VerifyAccessToken(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "访问令牌无效"})
		return auth.Principal{}, false
	}
	return principal, true
}

func writePersonaError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrPersonaDenied), errors.Is(err, service.ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "无权访问该组织人设"})
	case errors.Is(err, service.ErrPersonaNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "组织尚未配置人设"})
	case errors.Is(err, service.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "组织不存在"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务暂时不可用"})
	}
}
