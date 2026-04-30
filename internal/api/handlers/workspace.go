package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// WorkspaceHandler 暴露应用工作目录的只读 HTTP 接口。
type WorkspaceHandler struct {
	service workspaceService
	tokens  *auth.TokenManager
}

type workspaceService interface {
	List(ctx context.Context, principal auth.Principal, appID, relative string) (service.WorkspaceListing, error)
	Download(ctx context.Context, principal auth.Principal, appID, relative string) (io.ReadCloser, error)
	Archive(ctx context.Context, principal auth.Principal, appID, relative string) (io.ReadCloser, error)
}

// NewWorkspaceHandler 创建 handler。
func NewWorkspaceHandler(svc workspaceService, tokens *auth.TokenManager) *WorkspaceHandler {
	return &WorkspaceHandler{service: svc, tokens: tokens}
}

// RegisterWorkspaceRoutes 注册工作目录路由。
func RegisterWorkspaceRoutes(router gin.IRouter, handler *WorkspaceHandler) {
	group := router.Group("/api/v1/apps/:appId/workspace")
	group.GET("", handler.List)
	group.GET("/file", handler.Download)
	group.GET("/archive", handler.Archive)
}

// List 列出工作目录条目。
func (h *WorkspaceHandler) List(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	result, err := h.service.List(c.Request.Context(), principal, c.Param("appId"), c.Query("path"))
	if err != nil {
		writeWorkspaceError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// Download 下载文件。
func (h *WorkspaceHandler) Download(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	relative := c.Query("path")
	if relative == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 path 参数"})
		return
	}
	stream, err := h.service.Download(c.Request.Context(), principal, c.Param("appId"), relative)
	if err != nil {
		writeWorkspaceError(c, err)
		return
	}
	defer stream.Close()
	c.Header("Content-Type", "application/octet-stream")
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, stream); err != nil {
		// 流写入中途失败：日志省略，HTTP 已写头部，直接返回。
		return
	}
}

// Archive 把目录打包返回。
func (h *WorkspaceHandler) Archive(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	stream, err := h.service.Archive(c.Request.Context(), principal, c.Param("appId"), c.Query("path"))
	if err != nil {
		writeWorkspaceError(c, err)
		return
	}
	defer stream.Close()
	c.Header("Content-Type", "application/gzip")
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, stream); err != nil {
		return
	}
}

func (h *WorkspaceHandler) principal(c *gin.Context) (auth.Principal, bool) {
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

func writeWorkspaceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrWorkspaceForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "无权访问工作目录"})
	case errors.Is(err, service.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "应用不存在"})
	case errors.Is(err, service.ErrWorkspaceMissing):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "应用未关联节点或 adapter 未配置"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "工作目录暂不可用"})
	}
}
