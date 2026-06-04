package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	redactlog "oc-manager/internal/log"
	"oc-manager/internal/service"
)

// industryKnowledgeTokenHeader 是外部行业库上传入口使用的固定鉴权 header 名称。
const industryKnowledgeTokenHeader = "X-OC-Industry-Knowledge-Token"

// IndustryKnowledgeHandler 暴露平台级行业知识库管理接口和外部固定 token 上传入口。
type IndustryKnowledgeHandler struct {
	// service 承接行业库 CRUD、文件上传下载与外部上传落库等业务能力。
	service industryKnowledgeService
	// uploadToken 是外部上传入口的精确匹配固定 token；空值表示入口禁用。
	uploadToken string
}

// industryKnowledgeService 是行业知识库 handler 依赖的最小 service 能力集合。
type industryKnowledgeService interface {
	// ListIndustryKnowledgeBases 分页列出平台级行业知识库。
	ListIndustryKnowledgeBases(ctx context.Context, principal auth.Principal, page, pageSize int32, keyword string) (service.IndustryKnowledgeBaseListResult, error)
	// CreateIndustryKnowledgeBase 创建平台级行业知识库。
	CreateIndustryKnowledgeBase(ctx context.Context, principal auth.Principal, name string) (service.IndustryKnowledgeBaseResult, error)
	// RenameIndustryKnowledgeBase 重命名平台级行业知识库。
	RenameIndustryKnowledgeBase(ctx context.Context, principal auth.Principal, id, name string) (service.IndustryKnowledgeBaseResult, error)
	// DeleteIndustryKnowledgeBase 删除未被助手版本引用的行业知识库。
	DeleteIndustryKnowledgeBase(ctx context.Context, principal auth.Principal, id string) error
	// ListIndustryFiles 分页列出指定行业知识库下的 RAGFlow 文件。
	ListIndustryFiles(ctx context.Context, principal auth.Principal, industryID string, page, pageSize int32, keyword, status string) (service.KnowledgeListResult, error)
	// SaveIndustryFile 保存平台管理员上传的行业知识库文件。
	SaveIndustryFile(ctx context.Context, principal auth.Principal, industryID, filename string, content io.Reader, size int64) (service.KnowledgeDocumentResult, error)
	// OpenIndustryFile 打开行业知识库文件下载流。
	OpenIndustryFile(ctx context.Context, principal auth.Principal, industryID, documentID string) (io.ReadCloser, int64, string, error)
	// DeleteIndustryFile 删除指定行业知识库文件。
	DeleteIndustryFile(ctx context.Context, principal auth.Principal, industryID, documentID string) error
	// ReparseIndustryFile 重新触发指定行业知识库文件解析。
	ReparseIndustryFile(ctx context.Context, principal auth.Principal, industryID, documentID string) (service.KnowledgeDocumentResult, error)
	// ExternalUploadIndustryFile 按行业名称接收外部系统上传的文件。
	ExternalUploadIndustryFile(ctx context.Context, industryName, filename string, content io.Reader, size int64) (service.KnowledgeDocumentResult, error)
}

// NewIndustryKnowledgeHandler 创建行业知识库 handler。
func NewIndustryKnowledgeHandler(svc industryKnowledgeService, uploadToken string) *IndustryKnowledgeHandler {
	return &IndustryKnowledgeHandler{service: svc, uploadToken: uploadToken}
}

// RegisterExternalIndustryKnowledgeRoutes 注册无需用户登录的外部行业知识库上传路由。
func RegisterExternalIndustryKnowledgeRoutes(router gin.IRouter, handler *IndustryKnowledgeHandler) {
	router.POST("/api/v1/external/industry-knowledge/files", handler.ExternalUpload)
}

// RegisterIndustryKnowledgeRoutes 注册受用户鉴权保护的平台行业知识库管理路由。
func RegisterIndustryKnowledgeRoutes(router gin.IRouter, handler *IndustryKnowledgeHandler) {
	group := router.Group("/api/v1/industry-knowledge-bases")
	group.GET("", handler.ListBases)
	group.POST("", handler.CreateBase)
	group.PUT("/:industryId", handler.RenameBase)
	group.DELETE("/:industryId", handler.DeleteBase)

	files := group.Group("/:industryId/knowledge")
	files.GET("", handler.ListFiles)
	files.POST("", handler.SaveFile)
	files.GET("/:documentId/file", handler.DownloadFile)
	files.DELETE("/:documentId", handler.DeleteFile)
	files.POST("/:documentId/reparse", handler.ReparseFile)
}

// ExternalUpload 接收外部系统按行业名称上传的知识库文件。
//
// @Summary      外部上传行业知识库文件
// @Description  使用固定 header token 鉴权，通过 multipart/form-data 提交 industry_name 与 file
// @Tags         industry-knowledge
// @Accept       multipart/form-data
// @Produce      json
// @Param        X-OC-Industry-Knowledge-Token  header    string  true  "固定上传鉴权字符串"
// @Param        industry_name                  formData  string  true  "行业名称"
// @Param        file                           formData  file    true  "上传文件"
// @Success      202                            {object}  service.KnowledgeDocumentResult
// @Failure      400                            {object}  ErrorResponse
// @Failure      401                            {object}  ErrorResponse
// @Failure      503                            {object}  ErrorResponse
// @Router       /external/industry-knowledge/files [post]
func (h *IndustryKnowledgeHandler) ExternalUpload(c *gin.Context) {
	if h.uploadToken == "" || c.GetHeader(industryKnowledgeTokenHeader) != h.uploadToken {
		c.JSON(http.StatusUnauthorized, apierror.New("INDUSTRY_KNOWLEDGE_TOKEN_INVALID", "行业知识库上传鉴权失败"))
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxKnowledgeUploadBytes+maxKnowledgeMultipartOverheadBytes)
	if err := c.Request.ParseMultipartForm(maxKnowledgeMultipartOverheadBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", maxKnowledgeUploadMessage))
			return
		}
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "上传文件格式不合法"))
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}

	industryName := c.Request.FormValue("industry_name")
	if industryName == "" {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "缺少 industry_name 参数"))
		return
	}
	file, fileHeader, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "缺少 file 文件"))
		return
	}
	defer file.Close()
	if fileHeader.Size > maxKnowledgeUploadBytes {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", maxKnowledgeUploadMessage))
		return
	}

	result, err := h.service.ExternalUploadIndustryFile(c.Request.Context(), industryName, fileHeader.Filename, file, fileHeader.Size)
	if err != nil {
		writeIndustryKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, result)
}

// ListBases 列出平台级行业知识库。
func (h *IndustryKnowledgeHandler) ListBases(c *gin.Context) {
	result, err := h.service.ListIndustryKnowledgeBases(c.Request.Context(), principalFromCtx(c), queryKnowledgeInt32(c, "page", 1), queryKnowledgeInt32(c, "page_size", 50), c.Query("keyword"))
	if err != nil {
		writeIndustryKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// CreateBase 创建平台级行业知识库。
func (h *IndustryKnowledgeHandler) CreateBase(c *gin.Context) {
	var req CreateIndustryKnowledgeBaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	result, err := h.service.CreateIndustryKnowledgeBase(c.Request.Context(), principalFromCtx(c), req.Name)
	if err != nil {
		writeIndustryKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

// RenameBase 重命名平台级行业知识库。
func (h *IndustryKnowledgeHandler) RenameBase(c *gin.Context) {
	var req UpdateIndustryKnowledgeBaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	result, err := h.service.RenameIndustryKnowledgeBase(c.Request.Context(), principalFromCtx(c), c.Param("industryId"), req.Name)
	if err != nil {
		writeIndustryKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// DeleteBase 删除平台级行业知识库。
func (h *IndustryKnowledgeHandler) DeleteBase(c *gin.Context) {
	if err := h.service.DeleteIndustryKnowledgeBase(c.Request.Context(), principalFromCtx(c), c.Param("industryId")); err != nil {
		writeIndustryKnowledgeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListFiles 列出指定行业知识库下的文件。
func (h *IndustryKnowledgeHandler) ListFiles(c *gin.Context) {
	result, err := h.service.ListIndustryFiles(c.Request.Context(), principalFromCtx(c), c.Param("industryId"), queryKnowledgeInt32(c, "page", 1), queryKnowledgeInt32(c, "page_size", 50), c.Query("keyword"), c.Query("status"))
	if err != nil {
		writeIndustryKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// SaveFile 上传平台侧行业知识库文件。
func (h *IndustryKnowledgeHandler) SaveFile(c *gin.Context) {
	filename := c.Query("filename")
	if filename == "" {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "缺少 filename 参数"))
		return
	}
	size, ok := prepareKnowledgeOctetStreamUpload(c)
	if !ok {
		return
	}
	result, err := h.service.SaveIndustryFile(c.Request.Context(), principalFromCtx(c), c.Param("industryId"), filename, c.Request.Body, size)
	if err != nil {
		writeIndustryKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, result)
}

// DownloadFile 下载行业知识库文件原始内容。
func (h *IndustryKnowledgeHandler) DownloadFile(c *gin.Context) {
	reader, size, filename, err := h.service.OpenIndustryFile(c.Request.Context(), principalFromCtx(c), c.Param("industryId"), c.Param("documentId"))
	if err != nil {
		writeIndustryKnowledgeError(c, err)
		return
	}
	writeKnowledgeDownload(c, filename, reader, size)
}

// DeleteFile 删除行业知识库文件。
func (h *IndustryKnowledgeHandler) DeleteFile(c *gin.Context) {
	if err := h.service.DeleteIndustryFile(c.Request.Context(), principalFromCtx(c), c.Param("industryId"), c.Param("documentId")); err != nil {
		writeIndustryKnowledgeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ReparseFile 重新触发行业知识库文件解析。
func (h *IndustryKnowledgeHandler) ReparseFile(c *gin.Context) {
	result, err := h.service.ReparseIndustryFile(c.Request.Context(), principalFromCtx(c), c.Param("industryId"), c.Param("documentId"))
	if err != nil {
		writeIndustryKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, result)
}

// writeIndustryKnowledgeError 把行业知识库 service 哨兵错误映射到稳定 HTTP 错误码。
func writeIndustryKnowledgeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrKnowledgeForbidden):
		c.JSON(http.StatusForbidden, apierror.New("KNOWLEDGE_FORBIDDEN", "无权访问该知识库"))
	case errors.Is(err, service.ErrIndustryKnowledgeNotFound):
		c.JSON(http.StatusNotFound, apierror.New("INDUSTRY_KNOWLEDGE_NOT_FOUND", "行业知识库不存在"))
	case errors.Is(err, service.ErrIndustryKnowledgeNameTaken):
		c.JSON(http.StatusConflict, apierror.New("INDUSTRY_KNOWLEDGE_NAME_TAKEN", "行业知识库名称已存在"))
	case errors.Is(err, service.ErrIndustryKnowledgeInUse):
		c.JSON(http.StatusConflict, apierror.New("INDUSTRY_KNOWLEDGE_IN_USE", "行业知识库正在被助手版本引用，不可删除"))
	case errors.Is(err, service.ErrIndustryKnowledgeUploadTokenInvalid):
		c.JSON(http.StatusUnauthorized, apierror.New("INDUSTRY_KNOWLEDGE_TOKEN_INVALID", "行业知识库上传鉴权失败"))
	case errors.Is(err, service.ErrKnowledgeDatasetCreating):
		c.JSON(http.StatusServiceUnavailable, apierror.New("KNOWLEDGE_DATASET_CREATING", "知识库正在初始化，请稍后重试"))
	case errors.Is(err, service.ErrKnowledgeMissing):
		c.JSON(http.StatusServiceUnavailable, apierror.New("KNOWLEDGE_NOT_CONFIGURED", "知识库未配置"))
	case errors.Is(err, service.ErrConflict):
		c.JSON(http.StatusConflict, apierror.New("CONFLICT", validationServiceMessage(err, service.ErrConflict)))
	case errors.Is(err, service.ErrNotFound):
		c.JSON(http.StatusNotFound, apierror.New("NOT_FOUND", "资源不存在"))
	default:
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", redactlog.SafeErrorMessage(err)))
	}
}
