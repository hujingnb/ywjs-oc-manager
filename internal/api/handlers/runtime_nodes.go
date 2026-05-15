package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	redactlog "oc-manager/internal/log"
	"oc-manager/internal/service"
)

// RuntimeNodesHandler 提供平台管理员维度的 runtime 节点管理路由。
// agent 自身的注册与心跳由 AgentEndpointsHandler 提供，避免暴露在管理员路由前缀下。
type RuntimeNodesHandler struct {
	service runtimeNodeService
}

type runtimeNodeService interface {
	ListNodes(ctx context.Context, principal auth.Principal, limit, offset int32) ([]service.RuntimeNodeResult, error)
	GetNode(ctx context.Context, principal auth.Principal, nodeID string) (service.RuntimeNodeResult, error)
	SetNodeStatus(ctx context.Context, principal auth.Principal, nodeID, status string) (service.RuntimeNodeResult, error)
}

// NewRuntimeNodesHandler 创建 runtime node handler。
func NewRuntimeNodesHandler(service runtimeNodeService) *RuntimeNodesHandler {
	return &RuntimeNodesHandler{service: service}
}

// RegisterRuntimeNodeRoutes 注册管理员侧的 runtime 节点路由。
func RegisterRuntimeNodeRoutes(router gin.IRouter, handler *RuntimeNodesHandler) {
	group := router.Group("/api/v1/runtime-nodes")
	group.GET("", handler.List)
	group.GET("/:nodeId", handler.Get)
	group.POST("/:nodeId/disable", handler.Disable)
	group.POST("/:nodeId/enable", handler.Enable)
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
	principal := principalFromCtx(c)
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
	principal := principalFromCtx(c)
	result, err := h.service.GetNode(c.Request.Context(), principal, c.Param("nodeId"))
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
	principal := principalFromCtx(c)
	result, err := h.service.SetNodeStatus(c.Request.Context(), principal, c.Param("nodeId"), status)
	if err != nil {
		writeRuntimeNodeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"runtime_node": result})
}

func writeRuntimeNodeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrForbidden):
		c.JSON(http.StatusForbidden, apierror.New("FORBIDDEN", "无权执行该操作"))
	case errors.Is(err, service.ErrNotFound):
		c.JSON(http.StatusNotFound, apierror.New("NOT_FOUND", "资源不存在"))
	case errors.Is(err, service.ErrAgentTokenInvalid):
		c.JSON(http.StatusUnauthorized, apierror.New("AGENT_TOKEN_INVALID", redactlog.SafeErrorMessage(err)))
	default:
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "服务暂时不可用"))
	}
}
