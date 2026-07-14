package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// AICCDispatchMetricsResponse 是平台管理员读取异步客服运行指标的稳定响应。
// 所有字段均为聚合计数或 gauge，不包含访客原文、token、会话或消息标识。
type AICCDispatchMetricsResponse struct {
	Counters    map[string]uint64 `json:"counters"`
	QueueWaitMS int64             `json:"queue_wait_ms"`
	Inflight    int64             `json:"inflight"`
	// QueueDepthByApp 的 map key 是隐藏 app ID；受控指标桥接器将其转换为 app_id 标签。
	QueueDepthByApp map[string]int64 `json:"queue_depth_by_app"`
	// InflightByApp 的 map key 与 QueueDepthByApp 一致，使 HPA 用同一 app_id selector 查询。
	InflightByApp map[string]int64 `json:"inflight_by_app"`
}

// AICCDispatchMetricsHandler 提供只读的异步客服指标快照。
type AICCDispatchMetricsHandler struct {
	metrics service.AICCDispatchMetricSource
}

// NewAICCDispatchMetricsHandler 创建指标读取 handler。
func NewAICCDispatchMetricsHandler(metrics service.AICCDispatchMetricSource) *AICCDispatchMetricsHandler {
	return &AICCDispatchMetricsHandler{metrics: metrics}
}

// RegisterAICCDispatchMetricsRoutes 注册平台级指标端点。
func RegisterAICCDispatchMetricsRoutes(router gin.IRouter, handler *AICCDispatchMetricsHandler) {
	router.GET("/api/v1/platform/aicc/metrics", handler.Get)
}

// Get 返回当前进程内异步客服指标快照。
//
// @Summary      查询客服异步消息指标
// @Description  平台管理员读取客服异步消息队列、重试、失败、熔断和租约恢复的安全聚合指标
// @Tags         platform
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  AICCDispatchMetricsResponse
// @Failure      401  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Router       /platform/aicc/metrics [get]
func (h *AICCDispatchMetricsHandler) Get(c *gin.Context) {
	principal := principalFromCtx(c)
	if !auth.CanViewPlatformUsage(principal) {
		apierror.JSON(c, http.StatusForbidden, "FORBIDDEN", apierror.MsgPlatformOverviewForbidden)
		return
	}
	snapshot := h.metrics.Metrics()
	c.JSON(http.StatusOK, AICCDispatchMetricsResponse{Counters: snapshot.Counters, QueueWaitMS: snapshot.QueueWaitMS, Inflight: snapshot.Inflight, QueueDepthByApp: snapshot.QueueDepthByApp, InflightByApp: snapshot.InflightByApp})
}
