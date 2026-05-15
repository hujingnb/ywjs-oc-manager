package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	redactlog "oc-manager/internal/log"
	"oc-manager/internal/service"
)

// KnowledgeHandler 暴露组织和应用维度的知识库 HTTP 接口。
type KnowledgeHandler struct {
	service knowledgeService
}

type knowledgeService interface {
	SaveOrgFile(ctx context.Context, principal auth.Principal, orgID, relative string, content io.Reader, size int64) error
	SaveAppFile(ctx context.Context, principal auth.Principal, orgID, appID, ownerUserID, relative string, content io.Reader, size int64) error
	DeleteOrgFile(ctx context.Context, principal auth.Principal, orgID, relative string) error
	DeleteAppFile(ctx context.Context, principal auth.Principal, orgID, appID, ownerUserID, relative string) error
	ListOrg(ctx context.Context, principal auth.Principal, orgID, relative string) (service.KnowledgeListResult, error)
	ListApp(ctx context.Context, principal auth.Principal, orgID, appID, ownerUserID, relative string) (service.KnowledgeListResult, error)
	GetOrgSyncStatus(ctx context.Context, principal auth.Principal, orgID string) ([]service.SyncStatusResult, error)
	RetryOrgNodeSync(ctx context.Context, principal auth.Principal, orgID, nodeID string) error
}

// NewKnowledgeHandler 创建 handler。
func NewKnowledgeHandler(svc knowledgeService) *KnowledgeHandler {
	return &KnowledgeHandler{service: svc}
}

// RegisterKnowledgeRoutes 注册路由。
// 组织层挂在 /organizations/:orgId/knowledge；应用层挂在 /apps/:appId/knowledge，
// app handler 通过 query 参数携带 owner_user_id。
func RegisterKnowledgeRoutes(router gin.IRouter, handler *KnowledgeHandler) {
	orgGroup := router.Group("/api/v1/organizations/:orgId/knowledge")
	orgGroup.GET("", handler.ListOrg)
	orgGroup.POST("", handler.SaveOrg)
	orgGroup.DELETE("", handler.DeleteOrg)
	orgGroup.GET("/sync-status", handler.GetOrgSyncStatus)
	orgGroup.POST("/sync-status/retry", handler.RetryOrgSync)

	appGroup := router.Group("/api/v1/apps/:appId/knowledge")
	appGroup.GET("", handler.ListApp)
	appGroup.POST("", handler.SaveApp)
	appGroup.DELETE("", handler.DeleteApp)
}

// GetOrgSyncStatus 列出组织在所有节点的最近同步状态。
// 仅组织管理员 / 平台管理员可调；返回 [{node_id, status, last_success_at, last_error, updated_at}]。
//
// @Summary      组织知识库同步状态
// @Description  列出组织知识库在所有 Runtime 节点的最近同步状态；仅组织管理员或平台管理员可调
// @Tags         knowledge
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path      string  true  "组织 ID"
// @Success      200    {object}  map[string][]service.SyncStatusResult
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /organizations/{orgId}/knowledge/sync-status [get]
func (h *KnowledgeHandler) GetOrgSyncStatus(c *gin.Context) {
	principal := principalFromCtx(c)
	statuses, err := h.service.GetOrgSyncStatus(c.Request.Context(), principal, c.Param("orgId"))
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"statuses": statuses})
}

// RetryOrgSync 触发指定 (org, node) 重新同步。
// body: {"node_id": "..."}；仅组织管理员 / 平台管理员可调。
//
// @Summary      重试组织知识库节点同步
// @Description  触发指定组织在指定 Runtime 节点上重新同步知识库
// @Tags         knowledge
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path      string                        true  "组织 ID"
// @Param        body   body      RetryOrgSyncRequest           true  "重试同步请求"
// @Success      202    {object}  map[string]string
// @Failure      400    {object}  ErrorResponse
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /organizations/{orgId}/knowledge/sync-status/retry [post]
func (h *KnowledgeHandler) RetryOrgSync(c *gin.Context) {
	principal := principalFromCtx(c)
	var body struct {
		NodeID string `json:"node_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		writeBindError(c, err)
		return
	}
	if err := h.service.RetryOrgNodeSync(c.Request.Context(), principal, c.Param("orgId"), body.NodeID); err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"status": "pending"})
}

// ListOrg 列出组织级知识库。
//
// @Summary      列出组织级知识库文件
// @Description  按 path 参数列出组织知识库指定目录的文件列表
// @Tags         knowledge
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path      string  true   "组织 ID"
// @Param        path   query     string  false  "目录路径"
// @Success      200    {object}  service.KnowledgeListResult
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /organizations/{orgId}/knowledge [get]
func (h *KnowledgeHandler) ListOrg(c *gin.Context) {
	principal := principalFromCtx(c)
	result, err := h.service.ListOrg(c.Request.Context(), principal, c.Param("orgId"), c.Query("path"))
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// SaveOrg 写入组织级文件。
//
// @Summary      上传组织级知识库文件
// @Description  通过 path query 参数指定目标路径，上传二进制内容写入组织知识库；Content-Length 必须正确设置
// @Tags         knowledge
// @Accept       application/octet-stream
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path      string  true  "组织 ID"
// @Param        path   query     string  true  "文件相对路径"
// @Success      204    "上传成功，无响应体"
// @Failure      400    {object}  ErrorResponse
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /organizations/{orgId}/knowledge [post]
func (h *KnowledgeHandler) SaveOrg(c *gin.Context) {
	principal := principalFromCtx(c)
	relative := c.Query("path")
	if relative == "" {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "缺少 path 参数"))
		return
	}
	size, _ := strconv.ParseInt(c.GetHeader("Content-Length"), 10, 64)
	if err := h.service.SaveOrgFile(c.Request.Context(), principal, c.Param("orgId"), relative, c.Request.Body, size); err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// DeleteOrg 删除组织级文件。
//
// @Summary      删除组织级知识库文件
// @Description  通过 path query 参数指定目标路径，从组织知识库中删除对应文件
// @Tags         knowledge
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path      string  true  "组织 ID"
// @Param        path   query     string  true  "文件相对路径"
// @Success      204    "删除成功，无响应体"
// @Failure      400    {object}  ErrorResponse
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /organizations/{orgId}/knowledge [delete]
func (h *KnowledgeHandler) DeleteOrg(c *gin.Context) {
	principal := principalFromCtx(c)
	relative := c.Query("path")
	if relative == "" {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "缺少 path 参数"))
		return
	}
	if err := h.service.DeleteOrgFile(c.Request.Context(), principal, c.Param("orgId"), relative); err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListApp 列出应用级知识库。
//
// @Summary      列出应用级知识库文件
// @Description  按 path 参数列出应用知识库指定目录的文件；需同时提供 org_id 和 owner_user_id
// @Tags         knowledge
// @Produce      json
// @Security     BearerAuth
// @Param        appId         path      string  true   "应用 ID"
// @Param        org_id        query     string  true   "应用所属组织 ID"
// @Param        owner_user_id query     string  true   "应用所有者用户 ID"
// @Param        path          query     string  false  "目录路径"
// @Success      200           {object}  service.KnowledgeListResult
// @Failure      400           {object}  ErrorResponse
// @Failure      401           {object}  ErrorResponse
// @Failure      403           {object}  ErrorResponse
// @Failure      503           {object}  ErrorResponse
// @Router       /apps/{appId}/knowledge [get]
func (h *KnowledgeHandler) ListApp(c *gin.Context) {
	principal := principalFromCtx(c)
	orgID := c.Query("org_id")
	owner := c.Query("owner_user_id")
	if orgID == "" || owner == "" {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "缺少 org_id 或 owner_user_id"))
		return
	}
	result, err := h.service.ListApp(c.Request.Context(), principal, orgID, c.Param("appId"), owner, c.Query("path"))
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// SaveApp 写入应用级文件。
//
// @Summary      上传应用级知识库文件
// @Description  通过 path query 参数指定目标路径，上传二进制内容写入应用知识库；需同时提供 org_id/owner_user_id/path
// @Tags         knowledge
// @Accept       application/octet-stream
// @Produce      json
// @Security     BearerAuth
// @Param        appId         path      string  true  "应用 ID"
// @Param        org_id        query     string  true  "应用所属组织 ID"
// @Param        owner_user_id query     string  true  "应用所有者用户 ID"
// @Param        path          query     string  true  "文件相对路径"
// @Success      204           "上传成功，无响应体"
// @Failure      400           {object}  ErrorResponse
// @Failure      401           {object}  ErrorResponse
// @Failure      403           {object}  ErrorResponse
// @Failure      503           {object}  ErrorResponse
// @Router       /apps/{appId}/knowledge [post]
func (h *KnowledgeHandler) SaveApp(c *gin.Context) {
	principal := principalFromCtx(c)
	orgID := c.Query("org_id")
	owner := c.Query("owner_user_id")
	relative := c.Query("path")
	if orgID == "" || owner == "" || relative == "" {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "缺少 org_id/owner_user_id/path"))
		return
	}
	size, _ := strconv.ParseInt(c.GetHeader("Content-Length"), 10, 64)
	if err := h.service.SaveAppFile(c.Request.Context(), principal, orgID, c.Param("appId"), owner, relative, c.Request.Body, size); err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// DeleteApp 删除应用级文件。
//
// @Summary      删除应用级知识库文件
// @Description  通过 path query 参数指定目标路径，从应用知识库中删除对应文件；需同时提供 org_id/owner_user_id/path
// @Tags         knowledge
// @Produce      json
// @Security     BearerAuth
// @Param        appId         path      string  true  "应用 ID"
// @Param        org_id        query     string  true  "应用所属组织 ID"
// @Param        owner_user_id query     string  true  "应用所有者用户 ID"
// @Param        path          query     string  true  "文件相对路径"
// @Success      204           "删除成功，无响应体"
// @Failure      400           {object}  ErrorResponse
// @Failure      401           {object}  ErrorResponse
// @Failure      403           {object}  ErrorResponse
// @Failure      503           {object}  ErrorResponse
// @Router       /apps/{appId}/knowledge [delete]
func (h *KnowledgeHandler) DeleteApp(c *gin.Context) {
	principal := principalFromCtx(c)
	orgID := c.Query("org_id")
	owner := c.Query("owner_user_id")
	relative := c.Query("path")
	if orgID == "" || owner == "" || relative == "" {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "缺少 org_id/owner_user_id/path"))
		return
	}
	if err := h.service.DeleteAppFile(c.Request.Context(), principal, orgID, c.Param("appId"), owner, relative); err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func writeKnowledgeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrKnowledgeForbidden):
		c.JSON(http.StatusForbidden, apierror.New("KNOWLEDGE_FORBIDDEN", "无权访问该知识库"))
	case errors.Is(err, service.ErrKnowledgeMissing):
		c.JSON(http.StatusServiceUnavailable, apierror.New("KNOWLEDGE_NOT_CONFIGURED", "知识库主副本未启用"))
	default:
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", redactlog.SafeErrorMessage(err)))
	}
}
