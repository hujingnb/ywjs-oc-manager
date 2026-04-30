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
