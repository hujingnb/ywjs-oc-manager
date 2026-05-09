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
	UpdateMaxApps(ctx context.Context, principal auth.Principal, nodeID string, maxApps *int32) (service.RuntimeNodeResult, error)
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
	group.PATCH("/:nodeId", handler.Patch)
	group.POST("/:nodeId/rotate-bootstrap", handler.RotateBootstrap)
	group.POST("/:nodeId/disable", handler.Disable)
	group.POST("/:nodeId/enable", handler.Enable)
}

// Create 平台管理员注册新节点，并返回一次性 bootstrap token。
//
// @Summary      注册 runtime 节点
// @Description  平台管理员注册新 runtime 节点，返回包含一次性 bootstrap token 的节点信息
// @Tags         runtime-nodes
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateRuntimeNodeRequest  true  "注册节点请求"
// @Success      201   {object}  map[string]service.RuntimeNodeResult
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      403   {object}  ErrorResponse
// @Failure      500   {object}  ErrorResponse
// @Router       /runtime-nodes [post]
func (h *RuntimeNodesHandler) Create(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	var req CreateRuntimeNodeRequest
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
//
// @Summary      runtime 节点列表
// @Description  平台管理员获取所有 runtime 节点，支持分页
// @Tags         runtime-nodes
// @Produce      json
// @Security     BearerAuth
// @Param        limit   query     int  false  "每页条数（默认不限）"
// @Param        offset  query     int  false  "分页偏移（默认 0）"
// @Success      200     {object}  map[string][]service.RuntimeNodeResult
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /runtime-nodes [get]
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
//
// @Summary      runtime 节点详情
// @Description  按 nodeId 获取单个 runtime 节点信息
// @Tags         runtime-nodes
// @Produce      json
// @Security     BearerAuth
// @Param        nodeId  path      string  true  "节点 ID"
// @Success      200     {object}  map[string]service.RuntimeNodeResult
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /runtime-nodes/{nodeId} [get]
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
//
// @Summary      轮换 bootstrap token
// @Description  为指定节点生成新的一次性 bootstrap token；节点处于活跃状态时返回 409
// @Tags         runtime-nodes
// @Produce      json
// @Security     BearerAuth
// @Param        nodeId  path      string  true  "节点 ID"
// @Success      200     {object}  map[string]service.RuntimeNodeResult
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      409     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /runtime-nodes/{nodeId}/rotate-bootstrap [post]
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

// Patch 更新节点的可调字段（v1.0.1 仅 max_apps）。
//
// @Summary      更新 runtime 节点
// @Description  更新节点的可调字段；当前仅支持 max_apps（null 表示清空上限）
// @Tags         runtime-nodes
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        nodeId  path      string                   true  "节点 ID"
// @Param        body    body      PatchRuntimeNodeRequest  true  "更新节点请求"
// @Success      200     {object}  map[string]service.RuntimeNodeResult
// @Failure      400     {object}  ErrorResponse
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /runtime-nodes/{nodeId} [patch]
func (h *RuntimeNodesHandler) Patch(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	var req PatchRuntimeNodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数不完整"})
		return
	}
	result, err := h.service.UpdateMaxApps(c.Request.Context(), principal, c.Param("nodeId"), req.MaxApps)
	if err != nil {
		writeRuntimeNodeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"runtime_node": result})
}

// Disable 禁用节点。
//
// @Summary      禁用 runtime 节点
// @Description  将节点状态设为 disabled，不再向该节点分配新应用
// @Tags         runtime-nodes
// @Produce      json
// @Security     BearerAuth
// @Param        nodeId  path      string  true  "节点 ID"
// @Success      200     {object}  map[string]service.RuntimeNodeResult
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /runtime-nodes/{nodeId}/disable [post]
func (h *RuntimeNodesHandler) Disable(c *gin.Context) {
	h.setStatus(c, domain.RuntimeNodeStatusDisabled)
}

// Enable 启用节点。
//
// @Summary      启用 runtime 节点
// @Description  将节点状态从 disabled 恢复为 active，允许继续分配应用
// @Tags         runtime-nodes
// @Produce      json
// @Security     BearerAuth
// @Param        nodeId  path      string  true  "节点 ID"
// @Success      200     {object}  map[string]service.RuntimeNodeResult
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /runtime-nodes/{nodeId}/enable [post]
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
