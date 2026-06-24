package handlers

// 知识库分片上传 handler：init / part / complete / abort 四步，org 与 app 两套。
// 大文件前端切成 ≤8MB 分片顺序上传，每个分片是短请求，规避公网入口对长上传的固定超时。

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
)

// parsePartNumber 解析路径里的分片序号，必须为 ≥1 的整数。
func parsePartNumber(c *gin.Context) (int32, bool) {
	raw := c.Param("partNumber")
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || n < 1 {
		apierror.JSON(c, http.StatusBadRequest, "BAD_REQUEST", apierror.MsgKnowledgePartNumberInvalid)
		return 0, false
	}
	return int32(n), true
}

// preparePartUpload 校验分片请求体大小并加 MaxBytesReader 上限；返回该分片字节数。
func preparePartUpload(c *gin.Context) (int64, bool) {
	size, ok := requestContentLength(c)
	if !ok {
		apierror.JSON(c, http.StatusBadRequest, "BAD_REQUEST", apierror.MsgKnowledgeMissingPartSize)
		return 0, false
	}
	if size > maxKnowledgePartBytes {
		apierror.JSON(c, http.StatusBadRequest, "BAD_REQUEST", apierror.MsgKnowledgePartTooLarge)
		return size, false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxKnowledgePartBytes)
	return size, true
}

// InitOrgUpload 发起企业知识库分片上传。
//
// @Summary      发起企业知识库分片上传
// @Description  大文件分片上传第一步：校验配额后返回 uploadId 与建议分片大小
// @Tags         knowledge
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgId    path      string                       true  "企业 ID"
// @Param        request  body      InitKnowledgeUploadRequest   true  "文件名与大小"
// @Success      200      {object}  service.KnowledgeUploadInitResult
// @Failure      400      {object}  ErrorResponse
// @Failure      403      {object}  ErrorResponse
// @Failure      409      {object}  ErrorResponse
// @Failure      503      {object}  ErrorResponse
// @Router       /organizations/{orgId}/knowledge-uploads [post]
func (h *KnowledgeHandler) InitOrgUpload(c *gin.Context) {
	var req InitKnowledgeUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierror.JSON(c, http.StatusBadRequest, "BAD_REQUEST", apierror.MsgKnowledgeInvalidRequest)
		return
	}
	result, err := h.service.InitOrgUpload(c.Request.Context(), principalFromCtx(c), c.Param("orgId"), req.Filename, req.Size)
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// UploadOrgPart 上传企业知识库分片。
//
// @Summary      上传企业知识库分片
// @Description  分片上传第二步：以 application/octet-stream 上传单个分片字节
// @Tags         knowledge
// @Accept       application/octet-stream
// @Produce      json
// @Security     BearerAuth
// @Param        orgId       path  string  true  "企业 ID"
// @Param        uploadId    path  string  true  "上传会话 ID"
// @Param        partNumber  path  int     true  "分片序号（从 1 起）"
// @Success      204
// @Failure      400  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /organizations/{orgId}/knowledge-uploads/{uploadId}/parts/{partNumber} [put]
func (h *KnowledgeHandler) UploadOrgPart(c *gin.Context) {
	partNumber, ok := parsePartNumber(c)
	if !ok {
		return
	}
	size, ok := preparePartUpload(c)
	if !ok {
		return
	}
	h.transferLimit.limitUploadBody(c)
	if err := h.service.UploadOrgPart(c.Request.Context(), principalFromCtx(c), c.Param("orgId"), c.Param("uploadId"), partNumber, c.Request.Body, size); err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// CompleteOrgUpload 合并企业知识库分片并触发解析。
//
// @Summary      完成企业知识库分片上传
// @Description  分片上传第三步：合并全部分片，推送 RAGFlow 并触发解析
// @Tags         knowledge
// @Produce      json
// @Security     BearerAuth
// @Param        orgId     path      string  true  "企业 ID"
// @Param        uploadId  path      string  true  "上传会话 ID"
// @Success      202       {object}  service.KnowledgeDocumentResult
// @Failure      400       {object}  ErrorResponse
// @Failure      403       {object}  ErrorResponse
// @Failure      404       {object}  ErrorResponse
// @Router       /organizations/{orgId}/knowledge-uploads/{uploadId}/complete [post]
func (h *KnowledgeHandler) CompleteOrgUpload(c *gin.Context) {
	result, err := h.service.CompleteOrgUpload(c.Request.Context(), principalFromCtx(c), c.Param("orgId"), c.Param("uploadId"))
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, result)
}

// AbortOrgUpload 中止企业知识库分片上传。
//
// @Summary      中止企业知识库分片上传
// @Description  分片上传可选步：取消会话并清理已上传分片
// @Tags         knowledge
// @Produce      json
// @Security     BearerAuth
// @Param        orgId     path  string  true  "企业 ID"
// @Param        uploadId  path  string  true  "上传会话 ID"
// @Success      204
// @Failure      403  {object}  ErrorResponse
// @Router       /organizations/{orgId}/knowledge-uploads/{uploadId} [delete]
func (h *KnowledgeHandler) AbortOrgUpload(c *gin.Context) {
	if err := h.service.AbortOrgUpload(c.Request.Context(), principalFromCtx(c), c.Param("orgId"), c.Param("uploadId")); err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// InitAppUpload 发起实例知识库分片上传。
//
// @Summary      发起实例知识库分片上传
// @Description  大文件分片上传第一步：校验配额后返回 uploadId 与建议分片大小
// @Tags         knowledge
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId    path      string                       true  "应用 ID"
// @Param        request  body      InitKnowledgeUploadRequest   true  "文件名与大小"
// @Success      200      {object}  service.KnowledgeUploadInitResult
// @Failure      400      {object}  ErrorResponse
// @Failure      403      {object}  ErrorResponse
// @Failure      409      {object}  ErrorResponse
// @Failure      503      {object}  ErrorResponse
// @Router       /apps/{appId}/knowledge-uploads [post]
func (h *KnowledgeHandler) InitAppUpload(c *gin.Context) {
	var req InitKnowledgeUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierror.JSON(c, http.StatusBadRequest, "BAD_REQUEST", apierror.MsgKnowledgeInvalidRequest)
		return
	}
	result, err := h.service.InitAppUpload(c.Request.Context(), principalFromCtx(c), c.Param("appId"), req.Filename, req.Size)
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// UploadAppPart 上传实例知识库分片。
//
// @Summary      上传实例知识库分片
// @Description  分片上传第二步：以 application/octet-stream 上传单个分片字节
// @Tags         knowledge
// @Accept       application/octet-stream
// @Produce      json
// @Security     BearerAuth
// @Param        appId       path  string  true  "应用 ID"
// @Param        uploadId    path  string  true  "上传会话 ID"
// @Param        partNumber  path  int     true  "分片序号（从 1 起）"
// @Success      204
// @Failure      400  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /apps/{appId}/knowledge-uploads/{uploadId}/parts/{partNumber} [put]
func (h *KnowledgeHandler) UploadAppPart(c *gin.Context) {
	partNumber, ok := parsePartNumber(c)
	if !ok {
		return
	}
	size, ok := preparePartUpload(c)
	if !ok {
		return
	}
	h.transferLimit.limitUploadBody(c)
	if err := h.service.UploadAppPart(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("uploadId"), partNumber, c.Request.Body, size); err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// CompleteAppUpload 合并实例知识库分片并触发解析。
//
// @Summary      完成实例知识库分片上传
// @Description  分片上传第三步：合并全部分片，推送 RAGFlow 并触发解析
// @Tags         knowledge
// @Produce      json
// @Security     BearerAuth
// @Param        appId     path      string  true  "应用 ID"
// @Param        uploadId  path      string  true  "上传会话 ID"
// @Success      202       {object}  service.KnowledgeDocumentResult
// @Failure      400       {object}  ErrorResponse
// @Failure      403       {object}  ErrorResponse
// @Failure      404       {object}  ErrorResponse
// @Router       /apps/{appId}/knowledge-uploads/{uploadId}/complete [post]
func (h *KnowledgeHandler) CompleteAppUpload(c *gin.Context) {
	result, err := h.service.CompleteAppUpload(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("uploadId"))
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, result)
}

// AbortAppUpload 中止实例知识库分片上传。
//
// @Summary      中止实例知识库分片上传
// @Description  分片上传可选步：取消会话并清理已上传分片
// @Tags         knowledge
// @Produce      json
// @Security     BearerAuth
// @Param        appId     path  string  true  "应用 ID"
// @Param        uploadId  path  string  true  "上传会话 ID"
// @Success      204
// @Failure      403  {object}  ErrorResponse
// @Router       /apps/{appId}/knowledge-uploads/{uploadId} [delete]
func (h *KnowledgeHandler) AbortAppUpload(c *gin.Context) {
	if err := h.service.AbortAppUpload(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("uploadId")); err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
