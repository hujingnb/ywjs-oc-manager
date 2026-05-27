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

// Search 执行当前实例 + 所属组织知识库检索。
//
// @Summary      Hermes 检索知识库
// @Description  通过 app runtime token 检索当前实例知识库和所属组织知识库，不接受 dataset ID
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
		c.JSON(http.StatusUnauthorized, apierror.New("INVALID_APP_TOKEN", "缺少 runtime token"))
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
// @Failure      503             {object}  ErrorResponse
// @Router       /runtime/knowledge/files [post]
func (h *RuntimeKnowledgeHandler) AddFile(c *gin.Context) {
	token := c.GetHeader(runtimeKnowledgeTokenHeader)
	if token == "" {
		c.JSON(http.StatusUnauthorized, apierror.New("INVALID_APP_TOKEN", "缺少 runtime token"))
		return
	}
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "缺少 file 字段"))
		return
	}
	stream, err := file.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "读取上传文件失败"))
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
