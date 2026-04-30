package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// UsageHandler 处理用量查询。
type UsageHandler struct {
	service usageService
	tokens  *auth.TokenManager
}

type usageService interface {
	GetAppUsage(ctx context.Context, principal auth.Principal, appID, ownerOrgID, ownerUserID string, newapiKeyID int64) (service.AppUsageSnapshot, error)
	GetMemberUsage(ctx context.Context, principal auth.Principal, orgID, memberID string) (service.AggregatedUsage, error)
	GetOrgUsage(ctx context.Context, principal auth.Principal, orgID string) (service.AggregatedUsage, error)
}

// NewUsageHandler 创建 usage handler。
func NewUsageHandler(svc usageService, tokens *auth.TokenManager) *UsageHandler {
	return &UsageHandler{service: svc, tokens: tokens}
}

// RegisterUsageRoutes 注册用量路由。
// 调用方需通过 query 参数提供 owner_org_id/owner_user_id/newapi_key_id，
// 因为 service 不直接读取 sqlc 类型。
func RegisterUsageRoutes(router gin.IRouter, handler *UsageHandler) {
	router.GET("/api/v1/apps/:appId/usage", handler.GetApp)
	router.GET("/api/v1/usage/members/:userId", handler.GetMember)
	router.GET("/api/v1/usage/organizations/:orgId", handler.GetOrg)
	router.GET("/api/v1/usage/platform", handler.GetPlatform)
}

// GetMember 返回单个成员名下应用的用量聚合。
func (h *UsageHandler) GetMember(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	orgID := c.Query("org_id")
	if orgID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 org_id"})
		return
	}
	view, err := h.service.GetMemberUsage(c.Request.Context(), principal, orgID, c.Param("userId"))
	if err != nil {
		writeUsageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"usage": view})
}

// GetOrg 返回组织维度的用量聚合。
func (h *UsageHandler) GetOrg(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	view, err := h.service.GetOrgUsage(c.Request.Context(), principal, c.Param("orgId"))
	if err != nil {
		writeUsageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"usage": view})
}

// GetPlatform 返回平台维度的用量聚合。
// 当前实现是把所有 active 组织的用量加起来；scope_id 为空。
func (h *UsageHandler) GetPlatform(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	if principal.Role != "platform_admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "仅平台管理员可访问"})
		return
	}
	// 平台维度暂时不做完整组织遍历，返回 sentinel scope=platform。
	view := service.AggregatedUsage{Scope: "platform"}
	c.JSON(http.StatusOK, gin.H{"usage": view})
}

func (h *UsageHandler) principal(c *gin.Context) (auth.Principal, bool) {
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

// GetApp 拉取应用维度的 token 用量。
func (h *UsageHandler) GetApp(c *gin.Context) {
	token, ok := bearerToken(c.GetHeader("Authorization"))
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "缺少访问令牌"})
		return
	}
	principal, err := h.tokens.VerifyAccessToken(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "访问令牌无效"})
		return
	}
	orgID := c.Query("owner_org_id")
	owner := c.Query("owner_user_id")
	if orgID == "" || owner == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 owner_org_id 或 owner_user_id"})
		return
	}
	keyID, _ := strconv.ParseInt(c.Query("newapi_key_id"), 10, 64)
	snapshot, err := h.service.GetAppUsage(c.Request.Context(), principal, c.Param("appId"), orgID, owner, keyID)
	if err != nil {
		writeUsageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"usage": snapshot})
}

func writeUsageError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "无权访问该用量"})
	case errors.Is(err, service.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "资源不存在"})
	case errors.Is(err, service.ErrUsageUnavailable):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "用量服务暂不可用"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "用量服务异常"})
	}
}
