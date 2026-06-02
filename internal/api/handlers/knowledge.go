package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	redactlog "oc-manager/internal/log"
	"oc-manager/internal/service"
)

// KnowledgeHandler 暴露组织和应用维度的 RAGFlow-backed 知识库 HTTP 接口。
type KnowledgeHandler struct {
	service knowledgeService
}

type knowledgeService interface {
	ListOrg(ctx context.Context, principal auth.Principal, orgID string, page, pageSize int32, keyword, status string) (service.KnowledgeListResult, error)
	SaveOrgFile(ctx context.Context, principal auth.Principal, orgID, filename string, content io.Reader, size int64) (service.KnowledgeDocumentResult, error)
	OpenOrgFile(ctx context.Context, principal auth.Principal, orgID, documentID string) (io.ReadCloser, int64, string, error)
	DeleteOrgFile(ctx context.Context, principal auth.Principal, orgID, documentID string) error
	ReparseOrgFile(ctx context.Context, principal auth.Principal, orgID, documentID string) (service.KnowledgeDocumentResult, error)
	ListApp(ctx context.Context, principal auth.Principal, appID string, page, pageSize int32, keyword, status string) (service.KnowledgeListResult, error)
	SaveAppFile(ctx context.Context, principal auth.Principal, appID, filename string, content io.Reader, size int64) (service.KnowledgeDocumentResult, error)
	OpenAppFile(ctx context.Context, principal auth.Principal, appID, documentID string) (io.ReadCloser, int64, string, error)
	DeleteAppFile(ctx context.Context, principal auth.Principal, appID, documentID string) error
	ReparseAppFile(ctx context.Context, principal auth.Principal, appID, documentID string) (service.KnowledgeDocumentResult, error)
}

const (
	// maxKnowledgeUploadBytes 是 manager 知识库上传的服务端硬上限，前端 / 后端提示文案均由此推导。
	maxKnowledgeUploadBytes            int64 = 1024 * 1024 * 1024
	maxKnowledgeMultipartOverheadBytes int64 = 1 * 1024 * 1024
)

// maxKnowledgeUploadMessage 是超出上限时返回给客户端的统一提示，以 MB 为单位由
// maxKnowledgeUploadBytes 直接换算，避免修改上限后文案与实际限制漂移。
var maxKnowledgeUploadMessage = fmt.Sprintf("单文件最多支持 %dMB", maxKnowledgeUploadBytes/(1024*1024))

// NewKnowledgeHandler 创建 handler。
func NewKnowledgeHandler(svc knowledgeService) *KnowledgeHandler {
	return &KnowledgeHandler{service: svc}
}

// RegisterKnowledgeRoutes 注册扁平 document 维度的知识库路由。
func RegisterKnowledgeRoutes(router gin.IRouter, handler *KnowledgeHandler) {
	orgGroup := router.Group("/api/v1/organizations/:orgId/knowledge")
	orgGroup.GET("", handler.ListOrg)
	orgGroup.POST("", handler.SaveOrg)
	orgGroup.GET("/:documentId/file", handler.DownloadOrg)
	orgGroup.DELETE("/:documentId", handler.DeleteOrg)
	orgGroup.POST("/:documentId/reparse", handler.ReparseOrg)

	appGroup := router.Group("/api/v1/apps/:appId/knowledge")
	appGroup.GET("", handler.ListApp)
	appGroup.POST("", handler.SaveApp)
	appGroup.GET("/:documentId/file", handler.DownloadApp)
	appGroup.DELETE("/:documentId", handler.DeleteApp)
	appGroup.POST("/:documentId/reparse", handler.ReparseApp)
}

// ListOrg 列出组织级知识库文件。
//
// @Summary      列出企业级知识库文件
// @Description  以扁平 RAGFlow document 列表返回企业知识库文件
// @Tags         knowledge
// @Produce      json
// @Security     BearerAuth
// @Param        orgId       path      string  true   "企业 ID"
// @Param        page        query     int     false  "页码，从 1 开始"
// @Param        page_size   query     int     false  "每页数量"
// @Param        keyword     query     string  false  "文件名关键词"
// @Param        status      query     string  false  "解析状态"
// @Success      200         {object}  service.KnowledgeListResult
// @Failure      401         {object}  ErrorResponse
// @Failure      403         {object}  ErrorResponse
// @Failure      503         {object}  ErrorResponse
// @Router       /organizations/{orgId}/knowledge [get]
func (h *KnowledgeHandler) ListOrg(c *gin.Context) {
	result, err := h.service.ListOrg(c.Request.Context(), principalFromCtx(c), c.Param("orgId"), queryKnowledgeInt32(c, "page", 1), queryKnowledgeInt32(c, "page_size", 50), c.Query("keyword"), c.Query("status"))
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// SaveOrg 上传组织级知识库文件。
//
// @Summary      上传企业级知识库文件
// @Description  通过 filename query 指定文件名，上传后进入 RAGFlow 解析队列
// @Tags         knowledge
// @Accept       application/octet-stream
// @Produce      json
// @Security     BearerAuth
// @Param        orgId     path      string  true  "企业 ID"
// @Param        filename  query     string  true  "文件名"
// @Success      202       {object}  service.KnowledgeDocumentResult
// @Failure      400       {object}  ErrorResponse
// @Failure      401       {object}  ErrorResponse
// @Failure      403       {object}  ErrorResponse
// @Failure      503       {object}  ErrorResponse
// @Router       /organizations/{orgId}/knowledge [post]
func (h *KnowledgeHandler) SaveOrg(c *gin.Context) {
	filename := c.Query("filename")
	if filename == "" {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "缺少 filename 参数"))
		return
	}
	size, ok := prepareKnowledgeOctetStreamUpload(c)
	if !ok {
		return
	}
	result, err := h.service.SaveOrgFile(c.Request.Context(), principalFromCtx(c), c.Param("orgId"), filename, c.Request.Body, size)
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, result)
}

// DownloadOrg 下载组织级知识库文件。
//
// @Summary      下载企业级知识库文件
// @Description  按 documentId 下载 RAGFlow 中的原始文件
// @Tags         knowledge
// @Produce      application/octet-stream
// @Security     BearerAuth
// @Param        orgId       path      string  true  "企业 ID"
// @Param        documentId  path      string  true  "document ID"
// @Success      200         {string}  binary  "二进制文件流"
// @Failure      401         {object}  ErrorResponse
// @Failure      403         {object}  ErrorResponse
// @Failure      404         {object}  ErrorResponse
// @Failure      503         {object}  ErrorResponse
// @Router       /organizations/{orgId}/knowledge/{documentId}/file [get]
func (h *KnowledgeHandler) DownloadOrg(c *gin.Context) {
	reader, size, filename, err := h.service.OpenOrgFile(c.Request.Context(), principalFromCtx(c), c.Param("orgId"), c.Param("documentId"))
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	writeKnowledgeDownload(c, filename, reader, size)
}

// DeleteOrg 删除组织级知识库文件。
//
// @Summary      删除企业级知识库文件
// @Description  按 documentId 删除 RAGFlow document
// @Tags         knowledge
// @Produce      json
// @Security     BearerAuth
// @Param        orgId       path  string  true  "企业 ID"
// @Param        documentId  path  string  true  "document ID"
// @Success      204         "删除成功，无响应体"
// @Failure      401         {object}  ErrorResponse
// @Failure      403         {object}  ErrorResponse
// @Failure      404         {object}  ErrorResponse
// @Failure      503         {object}  ErrorResponse
// @Router       /organizations/{orgId}/knowledge/{documentId} [delete]
func (h *KnowledgeHandler) DeleteOrg(c *gin.Context) {
	if err := h.service.DeleteOrgFile(c.Request.Context(), principalFromCtx(c), c.Param("orgId"), c.Param("documentId")); err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ReparseOrg 重新解析组织级知识库文件。
//
// @Summary      重新解析企业级知识库文件
// @Description  按 documentId 重新触发 RAGFlow parse
// @Tags         knowledge
// @Produce      json
// @Security     BearerAuth
// @Param        orgId       path      string  true  "企业 ID"
// @Param        documentId  path      string  true  "document ID"
// @Success      202         {object}  service.KnowledgeDocumentResult
// @Failure      401         {object}  ErrorResponse
// @Failure      403         {object}  ErrorResponse
// @Failure      404         {object}  ErrorResponse
// @Failure      503         {object}  ErrorResponse
// @Router       /organizations/{orgId}/knowledge/{documentId}/reparse [post]
func (h *KnowledgeHandler) ReparseOrg(c *gin.Context) {
	result, err := h.service.ReparseOrgFile(c.Request.Context(), principalFromCtx(c), c.Param("orgId"), c.Param("documentId"))
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, result)
}

// ListApp 列出应用级知识库文件。
//
// @Summary      列出应用级知识库文件
// @Description  以扁平 RAGFlow document 列表返回实例知识库文件
// @Tags         knowledge
// @Produce      json
// @Security     BearerAuth
// @Param        appId       path      string  true   "实例 ID"
// @Param        page        query     int     false  "页码，从 1 开始"
// @Param        page_size   query     int     false  "每页数量"
// @Param        keyword     query     string  false  "文件名关键词"
// @Param        status      query     string  false  "解析状态"
// @Success      200         {object}  service.KnowledgeListResult
// @Failure      401         {object}  ErrorResponse
// @Failure      403         {object}  ErrorResponse
// @Failure      503         {object}  ErrorResponse
// @Router       /apps/{appId}/knowledge [get]
func (h *KnowledgeHandler) ListApp(c *gin.Context) {
	result, err := h.service.ListApp(c.Request.Context(), principalFromCtx(c), c.Param("appId"), queryKnowledgeInt32(c, "page", 1), queryKnowledgeInt32(c, "page_size", 50), c.Query("keyword"), c.Query("status"))
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// SaveApp 上传应用级知识库文件。
//
// @Summary      上传应用级知识库文件
// @Description  通过 filename query 指定文件名，上传后进入 RAGFlow 解析队列
// @Tags         knowledge
// @Accept       application/octet-stream
// @Produce      json
// @Security     BearerAuth
// @Param        appId     path      string  true  "实例 ID"
// @Param        filename  query     string  true  "文件名"
// @Success      202       {object}  service.KnowledgeDocumentResult
// @Failure      400       {object}  ErrorResponse
// @Failure      401       {object}  ErrorResponse
// @Failure      403       {object}  ErrorResponse
// @Failure      503       {object}  ErrorResponse
// @Router       /apps/{appId}/knowledge [post]
func (h *KnowledgeHandler) SaveApp(c *gin.Context) {
	filename := c.Query("filename")
	if filename == "" {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "缺少 filename 参数"))
		return
	}
	size, ok := prepareKnowledgeOctetStreamUpload(c)
	if !ok {
		return
	}
	result, err := h.service.SaveAppFile(c.Request.Context(), principalFromCtx(c), c.Param("appId"), filename, c.Request.Body, size)
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, result)
}

// DownloadApp 下载应用级知识库文件。
//
// @Summary      下载应用级知识库文件
// @Description  按 documentId 下载 RAGFlow 中的原始文件
// @Tags         knowledge
// @Produce      application/octet-stream
// @Security     BearerAuth
// @Param        appId       path      string  true  "实例 ID"
// @Param        documentId  path      string  true  "document ID"
// @Success      200         {string}  binary  "二进制文件流"
// @Failure      401         {object}  ErrorResponse
// @Failure      403         {object}  ErrorResponse
// @Failure      404         {object}  ErrorResponse
// @Failure      503         {object}  ErrorResponse
// @Router       /apps/{appId}/knowledge/{documentId}/file [get]
func (h *KnowledgeHandler) DownloadApp(c *gin.Context) {
	reader, size, filename, err := h.service.OpenAppFile(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("documentId"))
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	writeKnowledgeDownload(c, filename, reader, size)
}

// DeleteApp 删除应用级知识库文件。
//
// @Summary      删除应用级知识库文件
// @Description  按 documentId 删除 RAGFlow document
// @Tags         knowledge
// @Produce      json
// @Security     BearerAuth
// @Param        appId       path  string  true  "实例 ID"
// @Param        documentId  path  string  true  "document ID"
// @Success      204         "删除成功，无响应体"
// @Failure      401         {object}  ErrorResponse
// @Failure      403         {object}  ErrorResponse
// @Failure      404         {object}  ErrorResponse
// @Failure      503         {object}  ErrorResponse
// @Router       /apps/{appId}/knowledge/{documentId} [delete]
func (h *KnowledgeHandler) DeleteApp(c *gin.Context) {
	if err := h.service.DeleteAppFile(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("documentId")); err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ReparseApp 重新解析应用级知识库文件。
//
// @Summary      重新解析应用级知识库文件
// @Description  按 documentId 重新触发 RAGFlow parse
// @Tags         knowledge
// @Produce      json
// @Security     BearerAuth
// @Param        appId       path      string  true  "实例 ID"
// @Param        documentId  path      string  true  "document ID"
// @Success      202         {object}  service.KnowledgeDocumentResult
// @Failure      401         {object}  ErrorResponse
// @Failure      403         {object}  ErrorResponse
// @Failure      404         {object}  ErrorResponse
// @Failure      503         {object}  ErrorResponse
// @Router       /apps/{appId}/knowledge/{documentId}/reparse [post]
func (h *KnowledgeHandler) ReparseApp(c *gin.Context) {
	result, err := h.service.ReparseAppFile(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("documentId"))
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, result)
}

// writeKnowledgeDownload 负责设置下载响应头、写出二进制流，并统一接管流关闭。
func writeKnowledgeDownload(c *gin.Context, filename string, stream io.ReadCloser, size int64) {
	defer stream.Close()
	c.Header("Content-Type", "application/octet-stream")
	if size >= 0 {
		c.Header("Content-Length", strconv.FormatInt(size, 10))
	}
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(filename)}))
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, stream); err != nil {
		c.Error(err)
	}
}

func queryKnowledgeInt32(c *gin.Context, key string, fallback int32) int32 {
	raw := c.Query(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return fallback
	}
	return int32(value)
}

func prepareKnowledgeOctetStreamUpload(c *gin.Context) (int64, bool) {
	size := requestContentLength(c)
	if size > maxKnowledgeUploadBytes {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", maxKnowledgeUploadMessage))
		return size, false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxKnowledgeUploadBytes)
	return size, true
}

func requestContentLength(c *gin.Context) int64 {
	if raw := c.GetHeader("Content-Length"); raw != "" {
		if size, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return size
		}
	}
	if c.Request.ContentLength > 0 {
		return c.Request.ContentLength
	}
	return 0
}

func writeKnowledgeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrKnowledgeForbidden):
		c.JSON(http.StatusForbidden, apierror.New("KNOWLEDGE_FORBIDDEN", "无权访问该知识库"))
	case errors.Is(err, service.ErrInvalidToken):
		c.JSON(http.StatusUnauthorized, apierror.New("INVALID_APP_TOKEN", "runtime token 无效"))
	case errors.Is(err, service.ErrNotFound):
		c.JSON(http.StatusNotFound, apierror.New("NOT_FOUND", "资源不存在"))
	case errors.Is(err, service.ErrKnowledgeDatasetCreating):
		c.JSON(http.StatusServiceUnavailable, apierror.New("KNOWLEDGE_DATASET_CREATING", "知识库正在初始化，请稍后重试"))
	case errors.Is(err, service.ErrKnowledgeMissing):
		c.JSON(http.StatusServiceUnavailable, apierror.New("KNOWLEDGE_NOT_CONFIGURED", "知识库未配置"))
	default:
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", redactlog.SafeErrorMessage(err)))
	}
}
