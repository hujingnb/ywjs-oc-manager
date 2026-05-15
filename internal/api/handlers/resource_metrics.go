package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// ResourceMetricsHandler 提供 runtime 节点和应用实例资源指标查询路由。
type ResourceMetricsHandler struct {
	// service 负责权限校验、时间范围归一化后的数据库查询和 DTO 映射。
	service resourceMetricsService
}

// resourceMetricsService 抽象资源指标 handler 依赖，便于 handler 单元测试注入 stub。
type resourceMetricsService interface {
	ListNodeResources(ctx context.Context, principal auth.Principal, nodeID string, r service.ResourceTimeRange) ([]service.NodeResourceSampleResult, error)
	ListNodeInstances(ctx context.Context, principal auth.Principal, nodeID string, limit, offset int32) ([]service.NodeInstanceResult, error)
	ListNodeInstanceResources(ctx context.Context, principal auth.Principal, nodeID, appID string, r service.ResourceTimeRange) ([]service.InstanceResourceSampleResult, error)
	ListAppResources(ctx context.Context, principal auth.Principal, appID string, r service.ResourceTimeRange) ([]service.InstanceResourceSampleResult, error)
}

// NodeResourceSamplesResponse 是节点资源趋势响应包装，固定使用 samples 字段。
type NodeResourceSamplesResponse struct {
	// Samples 是按时间升序返回的节点资源采样或聚合桶。
	Samples []service.NodeResourceSampleResult `json:"samples"`
}

// InstanceResourceSamplesResponse 是实例资源趋势响应包装，固定使用 samples 字段。
type InstanceResourceSamplesResponse struct {
	// Samples 是按时间升序返回的实例资源采样或聚合桶。
	Samples []service.InstanceResourceSampleResult `json:"samples"`
}

// NodeInstancesResponse 是节点实例列表响应包装，固定使用 instances 字段。
type NodeInstancesResponse struct {
	// Instances 是节点上承载的应用实例摘要。
	Instances []service.NodeInstanceResult `json:"instances"`
}

// NewResourceMetricsHandler 创建资源指标 handler。
func NewResourceMetricsHandler(svc resourceMetricsService) *ResourceMetricsHandler {
	return &ResourceMetricsHandler{service: svc}
}

// RegisterResourceMetricsRoutes 注册资源指标查询路由。
func RegisterResourceMetricsRoutes(router gin.IRouter, handler *ResourceMetricsHandler) {
	router.GET("/api/v1/runtime-nodes/:nodeId/resources", handler.NodeResources)
	router.GET("/api/v1/runtime-nodes/:nodeId/instances", handler.NodeInstances)
	router.GET("/api/v1/runtime-nodes/:nodeId/instances/:appId/resources", handler.NodeInstanceResources)
	router.GET("/api/v1/apps/:appId/resources", handler.AppResources)
}

// NodeResources 查询 runtime 节点资源趋势。
//
// @Summary      runtime 节点资源趋势
// @Description  平台管理员按时间范围查询节点 CPU、内存、磁盘、网络和实例数量指标
// @Tags         runtime-nodes
// @Produce      json
// @Security     BearerAuth
// @Param        nodeId  path      string  true   "节点 ID"
// @Param        from    query     string  false  "开始时间 RFC3339，默认最近 7 天"
// @Param        to      query     string  false  "结束时间 RFC3339，默认当前时间"
// @Param        bucket  query     string  false  "聚合粒度，可选 5m / 1h；为空返回原始采样"
// @Success      200     {object}  NodeResourceSamplesResponse
// @Failure      400     {object}  ErrorResponse
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /runtime-nodes/{nodeId}/resources [get]
func (h *ResourceMetricsHandler) NodeResources(c *gin.Context) {
	principal := principalFromCtx(c)
	resourceRange, ok := h.resourceRange(c)
	if !ok {
		return
	}
	samples, err := h.service.ListNodeResources(c.Request.Context(), principal, c.Param("nodeId"), resourceRange)
	if err != nil {
		writeResourceMetricsError(c, err)
		return
	}
	c.JSON(http.StatusOK, NodeResourceSamplesResponse{Samples: samples})
}

// NodeInstances 查询 runtime 节点上的应用实例列表。
//
// @Summary      runtime 节点实例列表
// @Description  平台管理员分页查看节点承载的应用实例及最近一次实例资源采样
// @Tags         runtime-nodes
// @Produce      json
// @Security     BearerAuth
// @Param        nodeId  path      string  true   "节点 ID"
// @Param        limit   query     int     false  "每页条数（默认 50，最大 200）"
// @Param        offset  query     int     false  "分页偏移（默认 0）"
// @Success      200     {object}  NodeInstancesResponse
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /runtime-nodes/{nodeId}/instances [get]
func (h *ResourceMetricsHandler) NodeInstances(c *gin.Context) {
	principal := principalFromCtx(c)
	instances, err := h.service.ListNodeInstances(c.Request.Context(), principal, c.Param("nodeId"), queryInt32(c, "limit", 0), queryInt32(c, "offset", 0))
	if err != nil {
		writeResourceMetricsError(c, err)
		return
	}
	c.JSON(http.StatusOK, NodeInstancesResponse{Instances: instances})
}

// NodeInstanceResources 查询指定节点上某个应用实例的资源趋势。
//
// @Summary      runtime 节点实例资源趋势
// @Description  平台管理员按节点和应用 ID 查询该实例资源趋势
// @Tags         runtime-nodes
// @Produce      json
// @Security     BearerAuth
// @Param        nodeId  path      string  true   "节点 ID"
// @Param        appId   path      string  true   "应用 ID"
// @Param        from    query     string  false  "开始时间 RFC3339，默认最近 7 天"
// @Param        to      query     string  false  "结束时间 RFC3339，默认当前时间"
// @Param        bucket  query     string  false  "聚合粒度，可选 5m / 1h；为空返回原始采样"
// @Success      200     {object}  InstanceResourceSamplesResponse
// @Failure      400     {object}  ErrorResponse
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /runtime-nodes/{nodeId}/instances/{appId}/resources [get]
func (h *ResourceMetricsHandler) NodeInstanceResources(c *gin.Context) {
	principal := principalFromCtx(c)
	resourceRange, ok := h.resourceRange(c)
	if !ok {
		return
	}
	samples, err := h.service.ListNodeInstanceResources(c.Request.Context(), principal, c.Param("nodeId"), c.Param("appId"), resourceRange)
	if err != nil {
		writeResourceMetricsError(c, err)
		return
	}
	c.JSON(http.StatusOK, InstanceResourceSamplesResponse{Samples: samples})
}

// AppResources 查询应用实例资源趋势。
//
// @Summary      应用资源趋势
// @Description  按应用 ID 查询实例资源趋势；权限沿用应用读取权限
// @Tags         apps
// @Produce      json
// @Security     BearerAuth
// @Param        appId   path      string  true   "应用 ID"
// @Param        from    query     string  false  "开始时间 RFC3339，默认最近 7 天"
// @Param        to      query     string  false  "结束时间 RFC3339，默认当前时间"
// @Param        bucket  query     string  false  "聚合粒度，可选 5m / 1h；为空返回原始采样"
// @Success      200     {object}  InstanceResourceSamplesResponse
// @Failure      400     {object}  ErrorResponse
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /apps/{appId}/resources [get]
func (h *ResourceMetricsHandler) AppResources(c *gin.Context) {
	principal := principalFromCtx(c)
	resourceRange, ok := h.resourceRange(c)
	if !ok {
		return
	}
	samples, err := h.service.ListAppResources(c.Request.Context(), principal, c.Param("appId"), resourceRange)
	if err != nil {
		writeResourceMetricsError(c, err)
		return
	}
	c.JSON(http.StatusOK, InstanceResourceSamplesResponse{Samples: samples})
}

// principal 从 Authorization Bearer token 提取调用主体。
// resourceRange 将 HTTP 查询参数转换为 service 层统一时间范围；非法范围直接返回 400。
func (h *ResourceMetricsHandler) resourceRange(c *gin.Context) (service.ResourceTimeRange, bool) {
	resourceRange, err := service.NormalizeResourceRange(c.Query("from"), c.Query("to"), c.Query("bucket"), time.Now().UTC())
	if err != nil {
		writeResourceMetricsError(c, err)
		return service.ResourceTimeRange{}, false
	}
	return resourceRange, true
}

// writeResourceMetricsError 将资源指标 service sentinel error 映射为 HTTP 状态码。
func writeResourceMetricsError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidResourceRange):
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_RESOURCE_RANGE", "资源查询范围不合法"))
	case errors.Is(err, service.ErrForbidden):
		c.JSON(http.StatusForbidden, apierror.New("FORBIDDEN", "无权执行该操作"))
	case errors.Is(err, service.ErrNotFound):
		c.JSON(http.StatusNotFound, apierror.New("NOT_FOUND", "资源不存在"))
	default:
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "服务暂时不可用"))
	}
}
