// Package handlers —— conversation_files.go 暴露对话文件上传/下载 HTTP 端点。
// 上传将文件存入 S3 并落库记录，返回元数据；下载通过预签名 URL 重定向客户端直取。
package handlers

import (
	"context"
	"io"
	"net/http"

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
	// Download 鉴权后生成预签名下载 URL 与原始文件名。
	Download(ctx context.Context, p auth.Principal, appID, sid, fileID string) (url, filename string, err error)
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
// @Summary      下载对话文件（重定向至预签名 URL）
// @Tags         hermes-conversations
// @Security     BearerAuth
// @Param        appId   path  string  true  "应用 ID"
// @Param        sid     path  string  true  "会话 ID"
// @Param        fileId  path  string  true  "文件 ID"
// @Success      302     "重定向至 S3 预签名 URL"
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/conversations/{sid}/files/{fileId} [get]
func (h *HermesConversationFileHandler) Download(c *gin.Context) {
	url, _, err := h.svc.Download(
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
	// 302 重定向让客户端直接从 S3 取文件，manager 不做中转。
	c.Redirect(http.StatusFound, url)
}
