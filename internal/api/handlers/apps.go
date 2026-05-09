package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// AppsHandler 暴露应用读取接口；写操作位于 onboarding 与 runtime operation handler。
type AppsHandler struct {
	service appService
	tokens  *auth.TokenManager
}

type appService interface {
	Get(ctx context.Context, principal auth.Principal, appID string) (service.AppResult, error)
	ListByOrg(ctx context.Context, principal auth.Principal, orgID string, limit, offset int32) ([]service.AppResult, error)
}

// NewAppsHandler 创建 handler。
func NewAppsHandler(svc appService, tokens *auth.TokenManager) *AppsHandler {
	return &AppsHandler{service: svc, tokens: tokens}
}

// RegisterAppRoutes 注册应用路由。
// 列表挂在组织维度 /organizations/:orgId/apps；详情挂在 /apps/:appId。
func RegisterAppRoutes(router gin.IRouter, handler *AppsHandler) {
	router.GET("/api/v1/organizations/:orgId/apps", handler.List)
	router.GET("/api/v1/apps/:appId", handler.Get)
}

// List 列出组织内的应用。
//
// @Summary      应用列表
// @Description  按组织 ID 分页列出应用；org_member 只能看到自己的应用
// @Tags         apps
// @Produce      json
// @Security     BearerAuth
// @Param        orgId   path      string  true   "组织 ID"
// @Param        limit   query     int     false  "每页条数（默认不限）"
// @Param        offset  query     int     false  "分页偏移（默认 0）"
// @Success      200     {object}  map[string][]service.AppResult
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /organizations/{orgId}/apps [get]
func (h *AppsHandler) List(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	limit := queryInt32(c, "limit", 0)
	offset := queryInt32(c, "offset", 0)
	results, err := h.service.ListByOrg(c.Request.Context(), principal, c.Param("orgId"), limit, offset)
	if err != nil {
		writeAppsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"apps": results})
}

// Get 查询单个应用详情。
//
// @Summary      应用详情
// @Description  按 appId 获取单个应用信息；org_member 只能查询自己的应用
// @Tags         apps
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "应用 ID"
// @Success      200    {object}  map[string]service.AppResult
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId} [get]
func (h *AppsHandler) Get(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	result, err := h.service.Get(c.Request.Context(), principal, c.Param("appId"))
	if err != nil {
		writeAppsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"app": result})
}

func (h *AppsHandler) principal(c *gin.Context) (auth.Principal, bool) {
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

func writeAppsError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "无权访问该应用"})
	case errors.Is(err, service.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "应用不存在"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务暂时不可用"})
	}
}
