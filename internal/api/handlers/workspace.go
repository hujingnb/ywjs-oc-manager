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

// WorkspaceHandler 暴露应用工作目录的只读 HTTP 接口。
type WorkspaceHandler struct {
	service workspaceService
}

type workspaceService interface {
	List(ctx context.Context, principal auth.Principal, appID, relative, keyword string) (service.WorkspaceListing, error)
	Download(ctx context.Context, principal auth.Principal, appID, relative string) (io.ReadCloser, error)
	Archive(ctx context.Context, principal auth.Principal, appID, relative string, w io.Writer) error
}

// NewWorkspaceHandler 创建 handler。
func NewWorkspaceHandler(svc workspaceService) *WorkspaceHandler {
	return &WorkspaceHandler{service: svc}
}

// RegisterWorkspaceRoutes 注册工作目录路由。
func RegisterWorkspaceRoutes(router gin.IRouter, handler *WorkspaceHandler) {
	group := router.Group("/api/v1/apps/:appId/workspace")
	group.GET("", handler.List)
	group.GET("/file", handler.Download)
	group.GET("/archive", handler.Archive)
}

// List 列出工作目录条目。
//
// @Summary      列出工作目录条目
// @Description  列出应用工作目录下的文件和子目录；path 为空时列出根目录；q 非空时忽略 path 并递归模糊搜索整个工作目录
// @Tags         workspace
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true   "应用 ID"
// @Param        path   query     string  false  "相对路径（默认根目录）"
// @Param        q      query     string  false  "模糊搜索关键字（非空时递归搜索整个工作目录，返回匹配文件的完整相对路径）"
// @Success      200    {object}  service.WorkspaceListing
// @Failure      400    {object}  ErrorResponse
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /apps/{appId}/workspace [get]
func (h *WorkspaceHandler) List(c *gin.Context) {
	principal := principalFromCtx(c)
	result, err := h.service.List(c.Request.Context(), principal, c.Param("appId"), c.Query("path"), c.Query("q"))
	if err != nil {
		writeWorkspaceError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// Download 下载文件。
//
// @Summary      下载工作目录文件
// @Description  从应用工作目录下载指定路径的单个文件，返回二进制流
// @Tags         workspace
// @Produce      application/octet-stream
// @Security     BearerAuth
// @Param        appId  path      string  true  "应用 ID"
// @Param        path   query     string  true  "文件相对路径"
// @Success      200    {string}  binary  "二进制文件流"
// @Failure      400    {object}  ErrorResponse
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /apps/{appId}/workspace/file [get]
func (h *WorkspaceHandler) Download(c *gin.Context) {
	principal := principalFromCtx(c)
	relative := c.Query("path")
	if relative == "" {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "缺少 path 参数"))
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

// Archive 把目录流式打成 zip 返回。
//
// Sprint 2 改用 scope-aware agent 端点，输出 zip（Sprint 1 agent 端实现）；
// service 直接把 zip 流写到 ResponseWriter，避免在 manager 进程缓冲。
//
// @Summary      下载工作目录归档
// @Description  将应用工作目录（或指定子目录）打包为 zip 并以流式返回；service 直接写入 ResponseWriter 避免缓冲
// @Tags         workspace
// @Produce      application/zip
// @Security     BearerAuth
// @Param        appId  path      string  true   "应用 ID"
// @Param        path   query     string  false  "归档起始路径（默认根目录）"
// @Success      200    {string}  binary  "二进制 zip 流"
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /apps/{appId}/workspace/archive [get]
func (h *WorkspaceHandler) Archive(c *gin.Context) {
	principal := principalFromCtx(c)
	c.Header("Content-Type", "application/zip")
	c.Status(http.StatusOK)
	if err := h.service.Archive(c.Request.Context(), principal,
		c.Param("appId"), c.Query("path"), c.Writer); err != nil {
		// 已发送 200 头，无法改 status；记录但 silent 关闭连接由 gin 处理。
		_ = err
		return
	}
}

func writeWorkspaceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrWorkspaceForbidden):
		c.JSON(http.StatusForbidden, apierror.New("WORKSPACE_FORBIDDEN", "无权访问工作目录"))
	case errors.Is(err, service.ErrNotFound):
		c.JSON(http.StatusNotFound, apierror.New("NOT_FOUND", "应用不存在"))
	case errors.Is(err, service.ErrWorkspaceMissing):
		c.JSON(http.StatusServiceUnavailable, apierror.New("WORKSPACE_NOT_CONFIGURED", "应用未关联节点或 adapter 未配置"))
	case errors.Is(err, service.ErrWorkspaceBadPath):
		c.JSON(http.StatusBadRequest, apierror.New("WORKSPACE_INVALID_PATH", "非法工作目录路径"))
	default:
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "工作目录暂不可用"))
	}
}
