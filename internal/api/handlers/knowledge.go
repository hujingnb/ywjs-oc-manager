package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// KnowledgeHandler 暴露组织和应用维度的知识库 HTTP 接口。
type KnowledgeHandler struct {
	service knowledgeService
	tokens  *auth.TokenManager
}

type knowledgeService interface {
	SaveOrgFile(ctx context.Context, principal auth.Principal, orgID, relative string, content io.Reader, size int64) error
	SaveAppFile(ctx context.Context, principal auth.Principal, orgID, appID, ownerUserID, relative string, content io.Reader, size int64) error
	DeleteOrgFile(ctx context.Context, principal auth.Principal, orgID, relative string) error
	DeleteAppFile(ctx context.Context, principal auth.Principal, orgID, appID, ownerUserID, relative string) error
	ListOrg(ctx context.Context, principal auth.Principal, orgID, relative string) (service.KnowledgeListResult, error)
	ListApp(ctx context.Context, principal auth.Principal, orgID, appID, ownerUserID, relative string) (service.KnowledgeListResult, error)
}

// NewKnowledgeHandler 创建 handler。
func NewKnowledgeHandler(svc knowledgeService, tokens *auth.TokenManager) *KnowledgeHandler {
	return &KnowledgeHandler{service: svc, tokens: tokens}
}

// RegisterKnowledgeRoutes 注册路由。
// 组织层挂在 /organizations/:orgId/knowledge；应用层挂在 /apps/:appId/knowledge，
// app handler 通过 query 参数携带 owner_user_id。
func RegisterKnowledgeRoutes(router gin.IRouter, handler *KnowledgeHandler) {
	orgGroup := router.Group("/api/v1/organizations/:orgId/knowledge")
	orgGroup.GET("", handler.ListOrg)
	orgGroup.POST("", handler.SaveOrg)
	orgGroup.DELETE("", handler.DeleteOrg)

	appGroup := router.Group("/api/v1/apps/:appId/knowledge")
	appGroup.GET("", handler.ListApp)
	appGroup.POST("", handler.SaveApp)
	appGroup.DELETE("", handler.DeleteApp)
}

// ListOrg 列出组织级知识库。
func (h *KnowledgeHandler) ListOrg(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	result, err := h.service.ListOrg(c.Request.Context(), principal, c.Param("orgId"), c.Query("path"))
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// SaveOrg 写入组织级文件。
func (h *KnowledgeHandler) SaveOrg(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	relative := c.Query("path")
	if relative == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 path 参数"})
		return
	}
	size, _ := strconv.ParseInt(c.GetHeader("Content-Length"), 10, 64)
	if err := h.service.SaveOrgFile(c.Request.Context(), principal, c.Param("orgId"), relative, c.Request.Body, size); err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// DeleteOrg 删除组织级文件。
func (h *KnowledgeHandler) DeleteOrg(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	relative := c.Query("path")
	if relative == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 path 参数"})
		return
	}
	if err := h.service.DeleteOrgFile(c.Request.Context(), principal, c.Param("orgId"), relative); err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListApp 列出应用级知识库。
func (h *KnowledgeHandler) ListApp(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	orgID := c.Query("org_id")
	owner := c.Query("owner_user_id")
	if orgID == "" || owner == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 org_id 或 owner_user_id"})
		return
	}
	result, err := h.service.ListApp(c.Request.Context(), principal, orgID, c.Param("appId"), owner, c.Query("path"))
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// SaveApp 写入应用级文件。
func (h *KnowledgeHandler) SaveApp(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	orgID := c.Query("org_id")
	owner := c.Query("owner_user_id")
	relative := c.Query("path")
	if orgID == "" || owner == "" || relative == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 org_id/owner_user_id/path"})
		return
	}
	size, _ := strconv.ParseInt(c.GetHeader("Content-Length"), 10, 64)
	if err := h.service.SaveAppFile(c.Request.Context(), principal, orgID, c.Param("appId"), owner, relative, c.Request.Body, size); err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// DeleteApp 删除应用级文件。
func (h *KnowledgeHandler) DeleteApp(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	orgID := c.Query("org_id")
	owner := c.Query("owner_user_id")
	relative := c.Query("path")
	if orgID == "" || owner == "" || relative == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 org_id/owner_user_id/path"})
		return
	}
	if err := h.service.DeleteAppFile(c.Request.Context(), principal, orgID, c.Param("appId"), owner, relative); err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *KnowledgeHandler) principal(c *gin.Context) (auth.Principal, bool) {
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

func writeKnowledgeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrKnowledgeForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "无权访问该知识库"})
	case errors.Is(err, service.ErrKnowledgeMissing):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "知识库主副本未启用"})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	}
}
