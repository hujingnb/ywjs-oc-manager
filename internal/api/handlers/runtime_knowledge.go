package handlers

import (
	"context"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/service"
)

const runtimeKnowledgeTokenHeader = "X-OC-App-Token"

// RuntimeKnowledgeHandler 暴露给 Hermes 容器内 oc-kb 使用的知识库 API。
type RuntimeKnowledgeHandler struct {
	service runtimeKnowledgeService
}

type runtimeKnowledgeService interface {
	RuntimeSearch(ctx context.Context, appToken, question string, topK int32) (service.KnowledgeSearchResult, error)
	RuntimeAddFile(ctx context.Context, appToken, filename string, content io.Reader, size int64) (service.KnowledgeDocumentResult, error)
}

// NewRuntimeKnowledgeHandler 创建 runtime knowledge handler。
func NewRuntimeKnowledgeHandler(svc runtimeKnowledgeService) *RuntimeKnowledgeHandler {
	return &RuntimeKnowledgeHandler{service: svc}
}

// RegisterRuntimeKnowledgeRoutes 注册无需用户登录态的 Hermes runtime API。
func RegisterRuntimeKnowledgeRoutes(router gin.IRouter, handler *RuntimeKnowledgeHandler) {
	group := router.Group("/api/v1/runtime/knowledge")
	group.POST("/search", handler.Search)
	group.POST("/files", handler.AddFile)
}

// Search 执行当前实例 + 所属企业知识库检索。
//
// @Summary      Hermes 检索知识库
// @Description  通过 app runtime token 检索当前实例知识库和所属企业知识库，不接受 dataset ID
// @Tags         runtime-knowledge
// @Accept       json
// @Produce      json
// @Param        X-OC-App-Token  header    string                         true  "实例 runtime token"
// @Param        body            body      RuntimeKnowledgeSearchRequest  true  "检索请求"
// @Success      200             {object}  service.KnowledgeSearchResult
// @Failure      400             {object}  ErrorResponse
// @Failure      401             {object}  ErrorResponse
// @Failure      503             {object}  ErrorResponse
// @Router       /runtime/knowledge/search [post]
func (h *RuntimeKnowledgeHandler) Search(c *gin.Context) {
	token := c.GetHeader(runtimeKnowledgeTokenHeader)
	if token == "" {
		apierror.JSON(c, http.StatusUnauthorized, "INVALID_APP_TOKEN", apierror.MsgKnowledgeMissingRuntimeToken)
		return
	}
	var body RuntimeKnowledgeSearchRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		writeBindError(c, err)
		return
	}
	result, err := h.service.RuntimeSearch(c.Request.Context(), token, body.Question, body.TopK)
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// AddFile 把 Hermes 工作目录中的报告添加到当前实例知识库。
//
// @Summary      Hermes 添加文件到实例知识库
// @Description  通过 app runtime token 将 Hermes 工作目录产物上传到当前实例 RAGFlow dataset
// @Tags         runtime-knowledge
// @Accept       multipart/form-data
// @Produce      json
// @Param        X-OC-App-Token  header    string  true  "实例 runtime token"
// @Param        file            formData  file    true  "要加入实例知识库的文件"
// @Success      202             {object}  service.KnowledgeDocumentResult
// @Failure      400             {object}  ErrorResponse
// @Failure      401             {object}  ErrorResponse
// @Failure      403             {object}  ErrorResponse
// @Failure      409             {object}  ErrorResponse
// @Failure      503             {object}  ErrorResponse
// @Router       /runtime/knowledge/files [post]
func (h *RuntimeKnowledgeHandler) AddFile(c *gin.Context) {
	token := c.GetHeader(runtimeKnowledgeTokenHeader)
	if token == "" {
		apierror.JSON(c, http.StatusUnauthorized, "INVALID_APP_TOKEN", apierror.MsgKnowledgeMissingRuntimeToken)
		return
	}
	maxBodyBytes := maxKnowledgeUploadBytes + maxKnowledgeMultipartOverheadBytes
	// multipart 会在解析后提供 file.Size；请求总大小未知时仍可依赖后续文件大小校验。
	if size, ok := requestContentLength(c); ok && size > maxBodyBytes {
		// 请求体声明大小已超限，用 i18n catalog key 传 MB 数值，支持双语本地化。
		apierror.JSON(c, http.StatusBadRequest, "BAD_REQUEST", apierror.MsgKnowledgeFileTooLarge, maxKnowledgeUploadMB)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
	file, err := c.FormFile("file")
	if err != nil {
		apierror.JSON(c, http.StatusBadRequest, "BAD_REQUEST", apierror.MsgKnowledgeMissingFileField)
		return
	}
	if file.Size > maxKnowledgeUploadBytes {
		// multipart 解析后实际文件大小超限，同样走 i18n 本地化。
		apierror.JSON(c, http.StatusBadRequest, "BAD_REQUEST", apierror.MsgKnowledgeFileTooLarge, maxKnowledgeUploadMB)
		return
	}
	stream, err := file.Open()
	if err != nil {
		apierror.JSON(c, http.StatusBadRequest, "BAD_REQUEST", apierror.MsgKnowledgeOpenFileFailed)
		return
	}
	defer stream.Close()
	result, err := h.service.RuntimeAddFile(c.Request.Context(), token, file.Filename, stream, file.Size)
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, result)
}
