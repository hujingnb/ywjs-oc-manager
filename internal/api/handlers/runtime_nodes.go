package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	redactlog "oc-manager/internal/log"
	"oc-manager/internal/service"
)

// RuntimeNodesHandler 提供平台管理员维度的 runtime 节点管理路由。
// agent 自身的注册与心跳由 AgentEndpointsHandler 提供，避免暴露在管理员路由前缀下。
type RuntimeNodesHandler struct {
	service runtimeNodeService
	tokens  *auth.TokenManager
}

type runtimeNodeService interface {
	CreateNode(ctx context.Context, principal auth.Principal, input service.RuntimeNodeInput) (service.RuntimeNodeResult, error)
	ListNodes(ctx context.Context, principal auth.Principal, limit, offset int32) ([]service.RuntimeNodeResult, error)
	GetNode(ctx context.Context, principal auth.Principal, nodeID string) (service.RuntimeNodeResult, error)
	RotateBootstrap(ctx context.Context, principal auth.Principal, nodeID string) (service.RuntimeNodeResult, error)
	SetNodeStatus(ctx context.Context, principal auth.Principal, nodeID, status string) (service.RuntimeNodeResult, error)
}

// NewRuntimeNodesHandler 创建 runtime node handler。
func NewRuntimeNodesHandler(service runtimeNodeService, tokens *auth.TokenManager) *RuntimeNodesHandler {
	return &RuntimeNodesHandler{service: service, tokens: tokens}
}

// RegisterRuntimeNodeRoutes 注册管理员侧的 runtime 节点路由。
func RegisterRuntimeNodeRoutes(router gin.IRouter, handler *RuntimeNodesHandler) {
	group := router.Group("/api/v1/runtime-nodes")
	group.GET("", handler.List)
	group.POST("", handler.Create)
	group.GET("/:nodeId", handler.Get)
	group.POST("/:nodeId/rotate-bootstrap", handler.RotateBootstrap)
	group.POST("/:nodeId/disable", handler.Disable)
	group.POST("/:nodeId/enable", handler.Enable)
}

type runtimeNodeRequest struct {
	Name                     string `json:"name" binding:"required"`
	HeartbeatIntervalSeconds int32  `json:"heartbeat_interval_seconds"`
	NodeDataRoot             string `json:"node_data_root"`
}

// Create 平台管理员注册新节点，并返回一次性 bootstrap token。
func (h *RuntimeNodesHandler) Create(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	var req runtimeNodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数不完整"})
		return
	}
	result, err := h.service.CreateNode(c.Request.Context(), principal, service.RuntimeNodeInput{
		Name:                     req.Name,
		HeartbeatIntervalSeconds: req.HeartbeatIntervalSeconds,
		NodeDataRoot:             req.NodeDataRoot,
	})
	if err != nil {
		writeRuntimeNodeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"runtime_node": result})
}

// List 列出 runtime 节点。
func (h *RuntimeNodesHandler) List(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	limit := queryInt32(c, "limit", 0)
	offset := queryInt32(c, "offset", 0)
	results, err := h.service.ListNodes(c.Request.Context(), principal, limit, offset)
	if err != nil {
		writeRuntimeNodeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"runtime_nodes": results})
}

// Get 获取节点详情。
func (h *RuntimeNodesHandler) Get(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	result, err := h.service.GetNode(c.Request.Context(), principal, c.Param("nodeId"))
	if err != nil {
		writeRuntimeNodeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"runtime_node": result})
}

// RotateBootstrap 给目标节点发放新的 bootstrap token。
func (h *RuntimeNodesHandler) RotateBootstrap(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	result, err := h.service.RotateBootstrap(c.Request.Context(), principal, c.Param("nodeId"))
	if err != nil {
		writeRuntimeNodeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"runtime_node": result})
}

// Disable 禁用节点。
func (h *RuntimeNodesHandler) Disable(c *gin.Context) {
	h.setStatus(c, domain.RuntimeNodeStatusDisabled)
}

// Enable 启用节点。
func (h *RuntimeNodesHandler) Enable(c *gin.Context) {
	h.setStatus(c, domain.RuntimeNodeStatusActive)
}

func (h *RuntimeNodesHandler) setStatus(c *gin.Context, status string) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	result, err := h.service.SetNodeStatus(c.Request.Context(), principal, c.Param("nodeId"), status)
	if err != nil {
		writeRuntimeNodeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"runtime_node": result})
}

func (h *RuntimeNodesHandler) principal(c *gin.Context) (auth.Principal, bool) {
	token, ok := bearerToken(c.GetHeader("Authorization"))
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "缺少访问令牌"})
		return auth.Principal{}, false
	}
	principal, err := h.tokens.VerifyAccessToken(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "访问令牌无效"})
		return auth.Principal{}, false
	}
	return principal, true
}

func writeRuntimeNodeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "无权执行该操作"})
	case errors.Is(err, service.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "资源不存在"})
	case errors.Is(err, service.ErrRuntimeNodeBusy):
		c.JSON(http.StatusConflict, gin.H{"error": "节点已注册，需先禁用再轮换 bootstrap"})
	case errors.Is(err, service.ErrBootstrapTokenInvalid),
		errors.Is(err, service.ErrAgentTokenInvalid):
		c.JSON(http.StatusUnauthorized, gin.H{"error": redactlog.SafeErrorMessage(err)})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务暂时不可用"})
	}
}
