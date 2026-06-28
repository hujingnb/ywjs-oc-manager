// Package handlers —— conversation_files.go 暴露对话文件上传/下载 HTTP 端点。
// 上传将文件存入 S3 并落库记录，返回元数据；下载由 manager 从 S3 流式回源代理返回，
// 避免浏览器直接访问集群内 S3 host（无法解析）或跨域问题。
package handlers

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// conversationFileService 是 handler 依赖的对话文件业务接口（窄接口，便于测试注入 fake）。
// 方法签名与 *service.ConversationFileService 完全一致。
type conversationFileService interface {
	// Upload 将文件写入 S3 并落库，返回文件元数据。
	Upload(ctx context.Context, p auth.Principal, appID, sid, filename string, body io.Reader, size int64) (service.ConversationFileUploadResult, error)
	// OpenFile 鉴权后打开文件只读流，供 handler 流式回源代理给浏览器。
	OpenFile(ctx context.Context, p auth.Principal, appID, sid, fileID string) (rc io.ReadCloser, filename, mime string, size int64, err error)
}

// HermesConversationFileHandler 处理 /api/v1/apps/:appId/hermes/conversations/:sid/files/* 路由。
type HermesConversationFileHandler struct {
	svc conversationFileService
}

// NewHermesConversationFileHandler 构造 handler。
func NewHermesConversationFileHandler(svc conversationFileService) *HermesConversationFileHandler {
	return &HermesConversationFileHandler{svc: svc}
}

// RegisterHermesConversationFileRoutes 注册对话文件上传/下载路由。
// 挂在与 RegisterHermesConversationRoutes 相同的路径前缀下，方便 router.go 统一装配。
func RegisterHermesConversationFileRoutes(router gin.IRouter, h *HermesConversationFileHandler) {
	g := router.Group("/api/v1/apps/:appId/hermes/conversations")
	g.POST("/:sid/files", h.Upload)
	g.GET("/:sid/files/:fileId", h.Download)
}

// writeConversationFileError 把对话文件 service 哨兵错误映射为统一 ErrorResponse。
// 具体规则见 request_errors.go 的 mappedServiceErrorRules（对话文件节）。
func writeConversationFileError(c *gin.Context, err error) {
	writeMappedServiceError(c, err, http.StatusInternalServerError, apierror.MsgInternal)
}

// Upload POST /api/v1/apps/{appId}/hermes/conversations/{sid}/files
//
// @Summary      上传对话文件
// @Tags         hermes-conversations
// @Accept       application/octet-stream
// @Produce      json
// @Security     BearerAuth
// @Param        appId     path   string  true   "应用 ID"
// @Param        sid       path   string  true   "会话 ID"
// @Param        filename  query  string  true   "原始文件名（含扩展名）"
// @Success      200       {object}  service.ConversationFileUploadResult
// @Failure      400       {object}  ErrorResponse
// @Failure      403       {object}  ErrorResponse
// @Failure      413       {object}  ErrorResponse
// @Failure      500       {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/conversations/{sid}/files [post]
func (h *HermesConversationFileHandler) Upload(c *gin.Context) {
	// filename 必填：用于推导 MIME、校验扩展名白名单并落库记录原始名称。
	filename := c.Query("filename")
	if filename == "" {
		apierror.JSON(c, http.StatusBadRequest, "CONVERSATION_FILE_BAD_REQUEST", apierror.MsgConversationFileBadRequest)
		return
	}

	// ContentLength 为 -1 时 service 层会在 PutObject 时按实际流长度处理；
	// 此处直接透传，由 service 做大小校验。
	res, err := h.svc.Upload(
		c.Request.Context(),
		principalFromCtx(c),
		c.Param("appId"),
		c.Param("sid"),
		filename,
		c.Request.Body,
		c.Request.ContentLength,
	)
	if err != nil {
		writeConversationFileError(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

// Download GET /api/v1/apps/{appId}/hermes/conversations/{sid}/files/{fileId}
//
// @Summary      下载对话文件（manager 流式回源代理）
// @Tags         hermes-conversations
// @Security     BearerAuth
// @Param        appId   path  string  true  "应用 ID"
// @Param        sid     path  string  true  "会话 ID"
// @Param        fileId  path  string  true  "文件 ID"
// @Success      200     {file}    binary
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/conversations/{sid}/files/{fileId} [get]
func (h *HermesConversationFileHandler) Download(c *gin.Context) {
	rc, filename, mimeType, size, err := h.svc.OpenFile(
		c.Request.Context(),
		principalFromCtx(c),
		c.Param("appId"),
		c.Param("sid"),
		c.Param("fileId"),
	)
	if err != nil {
		writeConversationFileError(c, err)
		return
	}
	defer rc.Close()
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	c.Header("Content-Type", mimeType)
	if size > 0 {
		c.Header("Content-Length", strconv.FormatInt(size, 10))
	}
	// RFC 5987 文件名编码，兼容非 ASCII；同时给浏览器 ASCII fallback。
	c.Header("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(filename))
	if _, err := io.Copy(c.Writer, rc); err != nil {
		// 响应头已发出，无法再改状态码；记录错误供日志查看。
		_ = c.Error(err)
	}
}
