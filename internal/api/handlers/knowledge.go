package handlers

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	redactlog "oc-manager/internal/log"
	"oc-manager/internal/service"
)

// KnowledgeHandler 暴露组织和应用维度的 RAGFlow-backed 知识库 HTTP 接口。
type KnowledgeHandler struct {
	// service 承接组织和应用知识库文件管理的业务能力。
	service knowledgeService
	// transferLimit 是文件上传下载的单请求限速配置；零值保持不限制。
	transferLimit TransferLimitConfig
}

type knowledgeService interface {
	ListOrg(ctx context.Context, principal auth.Principal, orgID string, page, pageSize int32, keyword, status string) (service.KnowledgeListResult, error)
	SaveOrgFile(ctx context.Context, principal auth.Principal, orgID, filename string, content io.Reader, size int64) (service.KnowledgeDocumentResult, error)
	OpenOrgFile(ctx context.Context, principal auth.Principal, orgID, documentID string) (io.ReadCloser, int64, string, error)
	DeleteOrgFile(ctx context.Context, principal auth.Principal, orgID, documentID string) error
	ClearOrgFiles(ctx context.Context, principal auth.Principal, orgID string) error
	ReparseOrgFile(ctx context.Context, principal auth.Principal, orgID, documentID string) (service.KnowledgeDocumentResult, error)
	ListApp(ctx context.Context, principal auth.Principal, appID string, page, pageSize int32, keyword, status string) (service.KnowledgeListResult, error)
	SaveAppFile(ctx context.Context, principal auth.Principal, appID, filename string, content io.Reader, size int64) (service.KnowledgeDocumentResult, error)
	OpenAppFile(ctx context.Context, principal auth.Principal, appID, documentID string) (io.ReadCloser, int64, string, error)
	DeleteAppFile(ctx context.Context, principal auth.Principal, appID, documentID string) error
	ReparseAppFile(ctx context.Context, principal auth.Principal, appID, documentID string) (service.KnowledgeDocumentResult, error)
	// 分片上传（大文件走 multipart，规避公网入口超时）：org 与 app 两套。
	InitOrgUpload(ctx context.Context, principal auth.Principal, orgID, filename string, size int64) (service.KnowledgeUploadInitResult, error)
	UploadOrgPart(ctx context.Context, principal auth.Principal, orgID, uploadID string, partNumber int32, body io.Reader, size int64) error
	CompleteOrgUpload(ctx context.Context, principal auth.Principal, orgID, uploadID string) (service.KnowledgeDocumentResult, error)
	AbortOrgUpload(ctx context.Context, principal auth.Principal, orgID, uploadID string) error
	InitAppUpload(ctx context.Context, principal auth.Principal, appID, filename string, size int64) (service.KnowledgeUploadInitResult, error)
	UploadAppPart(ctx context.Context, principal auth.Principal, appID, uploadID string, partNumber int32, body io.Reader, size int64) error
	CompleteAppUpload(ctx context.Context, principal auth.Principal, appID, uploadID string) (service.KnowledgeDocumentResult, error)
	AbortAppUpload(ctx context.Context, principal auth.Principal, appID, uploadID string) error
	ListKnowledgeEmbeddingModels(ctx context.Context, principal auth.Principal) (service.KnowledgeEmbeddingModelListResult, error)
	GetKnowledgeRAGFlowDatasetInfo(ctx context.Context, principal auth.Principal, scope, targetID string) (service.KnowledgeRAGFlowDatasetInfoResult, error)
	UpdateKnowledgeEmbeddingModel(ctx context.Context, principal auth.Principal, scope, targetID string, input service.KnowledgeEmbeddingModelInput) (service.KnowledgeRAGFlowDatasetInfoResult, error)
}

// knowledgeRAGFlowDatasetService 是 handler 查询和修改 RAGFlow dataset 运维信息所需的最小能力。
type knowledgeRAGFlowDatasetService interface {
	GetKnowledgeRAGFlowDatasetInfo(ctx context.Context, principal auth.Principal, scope, targetID string) (service.KnowledgeRAGFlowDatasetInfoResult, error)
	UpdateKnowledgeEmbeddingModel(ctx context.Context, principal auth.Principal, scope, targetID string, input service.KnowledgeEmbeddingModelInput) (service.KnowledgeRAGFlowDatasetInfoResult, error)
}

const (
	// maxKnowledgeUploadBytes 是 manager 知识库上传的服务端硬上限，前端 / 后端提示文案均由此推导。
	maxKnowledgeUploadBytes            int64 = 1024 * 1024 * 1024
	maxKnowledgeMultipartOverheadBytes int64 = 1 * 1024 * 1024
	// maxKnowledgePartBytes 是单个分片请求体的硬上限：前端按 8MB 切片，留足余量到 64MB，
	// 既防御异常超大分片，又不至于卡正常分片。
	maxKnowledgePartBytes int64 = 64 * 1024 * 1024
	// maxKnowledgeUploadMB 是 maxKnowledgeUploadBytes 换算为 MB 后的整数值，用于 i18n 错误消息的 %d 占位符；
	// 与 maxKnowledgeUploadBytes 保持同步，修改上限时两者同步调整。
	maxKnowledgeUploadMB int64 = maxKnowledgeUploadBytes / (1024 * 1024)
)

// NewKnowledgeHandler 创建 handler；limits 保持可选以兼容未配置限速的历史调用路径。
func NewKnowledgeHandler(svc knowledgeService, limits ...TransferLimitConfig) *KnowledgeHandler {
	var limit TransferLimitConfig
	if len(limits) > 0 {
		limit = limits[0]
	}
	return &KnowledgeHandler{service: svc, transferLimit: limit}
}

// RegisterKnowledgeRoutes 注册扁平 document 维度的知识库路由。
func RegisterKnowledgeRoutes(router gin.IRouter, handler *KnowledgeHandler) {
	router.GET("/api/v1/knowledge/embedding-models", handler.ListEmbeddingModels)

	orgGroup := router.Group("/api/v1/organizations/:orgId/knowledge")
	orgGroup.GET("/ragflow-dataset", handler.GetOrgRAGFlowDataset)
	orgGroup.PATCH("/ragflow-dataset/embedding-model", handler.UpdateOrgEmbeddingModel)
	orgGroup.GET("", handler.ListOrg)
	orgGroup.POST("", handler.SaveOrg)
	orgGroup.DELETE("", handler.ClearOrg)
	orgGroup.GET("/:documentId/file", handler.DownloadOrg)
	orgGroup.DELETE("/:documentId", handler.DeleteOrg)
	orgGroup.POST("/:documentId/reparse", handler.ReparseOrg)

	// 分片上传走独立路径前缀 knowledge-uploads，避免与 /knowledge/:documentId 的通配段在
	// gin 路由树里冲突（静态段 uploads 与 :documentId 不能同级）。
	orgUploads := router.Group("/api/v1/organizations/:orgId/knowledge-uploads")
	orgUploads.POST("", handler.InitOrgUpload)
	orgUploads.PUT("/:uploadId/parts/:partNumber", handler.UploadOrgPart)
	orgUploads.POST("/:uploadId/complete", handler.CompleteOrgUpload)
	orgUploads.DELETE("/:uploadId", handler.AbortOrgUpload)

	appGroup := router.Group("/api/v1/apps/:appId/knowledge")
	appGroup.GET("/ragflow-dataset", handler.GetAppRAGFlowDataset)
	appGroup.PATCH("/ragflow-dataset/embedding-model", handler.UpdateAppEmbeddingModel)
	appGroup.GET("", handler.ListApp)
	appGroup.POST("", handler.SaveApp)
	appGroup.GET("/:documentId/file", handler.DownloadApp)
	appGroup.DELETE("/:documentId", handler.DeleteApp)
	appGroup.POST("/:documentId/reparse", handler.ReparseApp)

	appUploads := router.Group("/api/v1/apps/:appId/knowledge-uploads")
	appUploads.POST("", handler.InitAppUpload)
	appUploads.PUT("/:uploadId/parts/:partNumber", handler.UploadAppPart)
	appUploads.POST("/:uploadId/complete", handler.CompleteAppUpload)
	appUploads.DELETE("/:uploadId", handler.AbortAppUpload)
}

// ListEmbeddingModels 列出平台可切换的 RAGFlow embedding 模型。
//
// @Summary      列出 RAGFlow embedding 模型
// @Description  平台管理员查看后端配置的 RAGFlow embedding 模型候选，用于切换 dataset 模型
// @Tags         knowledge
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  service.KnowledgeEmbeddingModelListResult
// @Failure      401  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      503  {object}  ErrorResponse
// @Router       /knowledge/embedding-models [get]
func (h *KnowledgeHandler) ListEmbeddingModels(c *gin.Context) {
	result, err := h.service.ListKnowledgeEmbeddingModels(c.Request.Context(), principalFromCtx(c))
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// GetOrgRAGFlowDataset 查看企业知识库对应的 RAGFlow dataset 运维信息。
//
// @Summary      查看企业 RAGFlow dataset
// @Description  平台管理员查看企业知识库对应的 RAGFlow dataset 状态、远端 ID、embedding 模型和文档统计
// @Tags         knowledge
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path      string  true  "企业 ID"
// @Success      200    {object}  service.KnowledgeRAGFlowDatasetInfoResult
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /organizations/{orgId}/knowledge/ragflow-dataset [get]
func (h *KnowledgeHandler) GetOrgRAGFlowDataset(c *gin.Context) {
	writeRAGFlowDatasetInfo(c, h.service, service.KnowledgeRAGFlowScopeOrg, c.Param("orgId"), writeKnowledgeError)
}

// UpdateOrgEmbeddingModel 修改企业知识库对应 RAGFlow dataset 的 embedding 模型。
//
// @Summary      修改企业 RAGFlow dataset embedding 模型
// @Description  平台管理员提交 RAGFlow 控制台可见的模型名和可选 provider，后端解析后切换企业 dataset 模型并触发重解析
// @Tags         knowledge
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path      string                                true  "企业 ID"
// @Param        body   body      UpdateKnowledgeEmbeddingModelRequest  true  "修改 embedding 模型请求"
// @Success      202    {object}  service.KnowledgeRAGFlowDatasetInfoResult
// @Failure      400    {object}  ErrorResponse
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /organizations/{orgId}/knowledge/ragflow-dataset/embedding-model [patch]
func (h *KnowledgeHandler) UpdateOrgEmbeddingModel(c *gin.Context) {
	updateRAGFlowDatasetEmbeddingModel(c, h.service, service.KnowledgeRAGFlowScopeOrg, c.Param("orgId"), writeKnowledgeError)
}

// GetAppRAGFlowDataset 查看应用知识库对应的 RAGFlow dataset 运维信息。
//
// @Summary      查看应用 RAGFlow dataset
// @Description  平台管理员查看应用知识库对应的 RAGFlow dataset 状态、远端 ID、embedding 模型和文档统计
// @Tags         knowledge
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "实例 ID"
// @Success      200    {object}  service.KnowledgeRAGFlowDatasetInfoResult
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /apps/{appId}/knowledge/ragflow-dataset [get]
func (h *KnowledgeHandler) GetAppRAGFlowDataset(c *gin.Context) {
	writeRAGFlowDatasetInfo(c, h.service, service.KnowledgeRAGFlowScopeApp, c.Param("appId"), writeKnowledgeError)
}

// UpdateAppEmbeddingModel 修改应用知识库对应 RAGFlow dataset 的 embedding 模型。
//
// @Summary      修改应用 RAGFlow dataset embedding 模型
// @Description  平台管理员提交 RAGFlow 控制台可见的模型名和可选 provider，后端解析后切换应用 dataset 模型并触发重解析
// @Tags         knowledge
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string                                true  "实例 ID"
// @Param        body   body      UpdateKnowledgeEmbeddingModelRequest  true  "修改 embedding 模型请求"
// @Success      202    {object}  service.KnowledgeRAGFlowDatasetInfoResult
// @Failure      400    {object}  ErrorResponse
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /apps/{appId}/knowledge/ragflow-dataset/embedding-model [patch]
func (h *KnowledgeHandler) UpdateAppEmbeddingModel(c *gin.Context) {
	updateRAGFlowDatasetEmbeddingModel(c, h.service, service.KnowledgeRAGFlowScopeApp, c.Param("appId"), writeKnowledgeError)
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
// @Failure      409       {object}  ErrorResponse
// @Failure      503       {object}  ErrorResponse
// @Router       /organizations/{orgId}/knowledge [post]
func (h *KnowledgeHandler) SaveOrg(c *gin.Context) {
	filename := c.Query("filename")
	if filename == "" {
		apierror.JSON(c, http.StatusBadRequest, "BAD_REQUEST", apierror.MsgKnowledgeMissingFilename)
		return
	}
	size, ok := prepareKnowledgeOctetStreamUpload(c)
	if !ok {
		return
	}
	h.transferLimit.limitUploadBody(c)
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
	writeKnowledgeDownload(c, filename, reader, size, h.transferLimit)
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

// ClearOrg 清空组织级知识库全部文件。
//
// @Summary      清空企业级知识库文件
// @Description  删除企业知识库中的全部 RAGFlow documents，保留企业和知识库 dataset
// @Tags         knowledge
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path  string  true  "企业 ID"
// @Success      204    "清空成功，无响应体"
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /organizations/{orgId}/knowledge [delete]
func (h *KnowledgeHandler) ClearOrg(c *gin.Context) {
	if err := h.service.ClearOrgFiles(c.Request.Context(), principalFromCtx(c), c.Param("orgId")); err != nil {
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
// @Failure      409       {object}  ErrorResponse
// @Failure      503       {object}  ErrorResponse
// @Router       /apps/{appId}/knowledge [post]
func (h *KnowledgeHandler) SaveApp(c *gin.Context) {
	filename := c.Query("filename")
	if filename == "" {
		apierror.JSON(c, http.StatusBadRequest, "BAD_REQUEST", apierror.MsgKnowledgeMissingFilename)
		return
	}
	size, ok := prepareKnowledgeOctetStreamUpload(c)
	if !ok {
		return
	}
	h.transferLimit.limitUploadBody(c)
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
	writeKnowledgeDownload(c, filename, reader, size, h.transferLimit)
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
func writeKnowledgeDownload(c *gin.Context, filename string, stream io.ReadCloser, size int64, limits ...TransferLimitConfig) {
	var limit TransferLimitConfig
	if len(limits) > 0 {
		limit = limits[0]
	}
	stream = limit.limitDownloadStream(c.Request.Context(), stream)
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
	// 知识库上传必须在进入 RAGFlow 前知道文件大小，否则无法做累计容量预校验。
	size, ok := requestContentLength(c)
	if !ok {
		apierror.JSON(c, http.StatusBadRequest, "BAD_REQUEST", apierror.MsgKnowledgeMissingFileSize)
		return 0, false
	}
	if size > maxKnowledgeUploadBytes {
		// 文件超限：用 i18n catalog key 传 MB 数值，支持双语本地化。
		apierror.JSON(c, http.StatusBadRequest, "BAD_REQUEST", apierror.MsgKnowledgeFileTooLarge, maxKnowledgeUploadMB)
		return size, false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxKnowledgeUploadBytes)
	return size, true
}

// writeRAGFlowDatasetInfo 统一读取不同知识库作用域的 RAGFlow dataset 运维信息。
func writeRAGFlowDatasetInfo(c *gin.Context, svc knowledgeRAGFlowDatasetService, scope, targetID string, writeErr func(*gin.Context, error)) {
	result, err := svc.GetKnowledgeRAGFlowDatasetInfo(c.Request.Context(), principalFromCtx(c), scope, targetID)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// updateRAGFlowDatasetEmbeddingModel 统一绑定模型切换请求，并只向 service 传递人类可读模型名和 provider。
func updateRAGFlowDatasetEmbeddingModel(c *gin.Context, svc knowledgeRAGFlowDatasetService, scope, targetID string, writeErr func(*gin.Context, error)) {
	var req UpdateKnowledgeEmbeddingModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierror.JSON(c, http.StatusBadRequest, "BAD_REQUEST", apierror.MsgKnowledgeModelNameRequired)
		return
	}
	input := service.KnowledgeEmbeddingModelInput{
		Name:     strings.TrimSpace(req.Name),
		Provider: strings.TrimSpace(req.Provider),
	}
	if input.Name == "" {
		apierror.JSON(c, http.StatusBadRequest, "BAD_REQUEST", apierror.MsgKnowledgeModelNameRequired)
		return
	}
	result, err := svc.UpdateKnowledgeEmbeddingModel(c.Request.Context(), principalFromCtx(c), scope, targetID, input)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusAccepted, result)
}

// requestContentLength 读取客户端声明的请求体大小；只有非负整数才可用于上传前容量校验。
func requestContentLength(c *gin.Context) (int64, bool) {
	if raw := c.GetHeader("Content-Length"); raw != "" {
		size, err := strconv.ParseInt(raw, 10, 64)
		return size, err == nil && size >= 0
	}
	if c.Request.ContentLength >= 0 {
		return c.Request.ContentLength, true
	}
	return 0, false
}

func writeKnowledgeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrKnowledgeForbidden):
		apierror.JSON(c, http.StatusForbidden, "KNOWLEDGE_FORBIDDEN", apierror.MsgKnowledgeForbidden)
	case errors.Is(err, service.ErrInvalidToken):
		apierror.JSON(c, http.StatusUnauthorized, "INVALID_APP_TOKEN", apierror.MsgKnowledgeRuntimeTokenInvalid)
	case errors.Is(err, service.ErrNotFound):
		apierror.JSON(c, http.StatusNotFound, "NOT_FOUND", apierror.MsgNotFound)
	case errors.Is(err, service.ErrKnowledgeDatasetCreating):
		apierror.JSON(c, http.StatusServiceUnavailable, "KNOWLEDGE_DATASET_CREATING", apierror.MsgKnowledgeDatasetCreating)
	case errors.Is(err, service.ErrKnowledgeMissing):
		apierror.JSON(c, http.StatusServiceUnavailable, "KNOWLEDGE_NOT_CONFIGURED", apierror.MsgKnowledgeNotConfigured)
	case errors.Is(err, service.ErrKnowledgeQuotaExceeded):
		// 配额明细由 service 错误链运行时拼接，属动态文案，保留原样不入 catalog。
		c.JSON(http.StatusConflict, apierror.New("KNOWLEDGE_QUOTA_EXCEEDED", validationServiceMessage(err, service.ErrKnowledgeQuotaExceeded)))
	case errors.Is(err, service.ErrKnowledgeMultipartUnavailable):
		// 未启用对象存储，分片上传不可用；前端据此回退到直传。
		apierror.JSON(c, http.StatusServiceUnavailable, "KNOWLEDGE_MULTIPART_UNAVAILABLE", apierror.MsgKnowledgeMultipartUnavailable)
	case errors.Is(err, service.ErrKnowledgeUploadSessionNotFound):
		// 会话不存在 / 已过期 / 归属不符，统一按 404 处理。
		apierror.JSON(c, http.StatusNotFound, "KNOWLEDGE_UPLOAD_SESSION_NOT_FOUND", apierror.MsgKnowledgeUploadSessionNotFound)
	default:
		// SafeErrorMessage 返回脱敏后的运行时错误明细，属动态文案，保留原样不入 catalog。
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", redactlog.SafeErrorMessage(err)))
	}
}
