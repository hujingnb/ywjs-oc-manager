package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// AppRuntimeHandler 暴露应用容器的高风险运行操作：start/stop/restart/delete。
type AppRuntimeHandler struct {
	service runtimeOperationService
	tokens  *auth.TokenManager
}

type runtimeOperationService interface {
	Trigger(ctx context.Context, principal auth.Principal, appID string, op service.RuntimeOperation) (service.RuntimeOperationResult, error)
	RequestInitialize(ctx context.Context, principal auth.Principal, appID string) (service.RuntimeOperationResult, error)
	InspectApp(ctx context.Context, principal auth.Principal, appID string) (service.RuntimeView, error)
}

// NewAppRuntimeHandler 创建 handler。
func NewAppRuntimeHandler(svc runtimeOperationService, tokens *auth.TokenManager) *AppRuntimeHandler {
	return &AppRuntimeHandler{service: svc, tokens: tokens}
}

// RegisterAppRuntimeRoutes 注册路由。
// 所有动作都通过 POST 触发，结果是 job_id，前端通过 jobs API 查询执行进度。
func RegisterAppRuntimeRoutes(router gin.IRouter, handler *AppRuntimeHandler) {
	group := router.Group("/api/v1/apps/:appId/runtime")
	group.POST("/start", handler.Start)
	group.POST("/stop", handler.Stop)
	group.POST("/restart", handler.Restart)
	group.POST("/delete", handler.Delete)
	router.POST("/api/v1/apps/:appId/initialize", handler.Initialize)
	router.GET("/api/v1/apps/:appId/runtime", handler.GetRuntime)
}

// Start 触发启动。
func (h *AppRuntimeHandler) Start(c *gin.Context)   { h.trigger(c, service.RuntimeOperationStart) }
// Stop 触发停止。
func (h *AppRuntimeHandler) Stop(c *gin.Context)    { h.trigger(c, service.RuntimeOperationStop) }
// Restart 触发重启。
func (h *AppRuntimeHandler) Restart(c *gin.Context) { h.trigger(c, service.RuntimeOperationRestart) }
// Delete 触发删除。
func (h *AppRuntimeHandler) Delete(c *gin.Context)  { h.trigger(c, service.RuntimeOperationDelete) }

// GetRuntime 返回应用容器的 inspect 视图。
// container_id 为空时直接返回 status="no_container"，避免无谓的 docker 调用。
func (h *AppRuntimeHandler) GetRuntime(c *gin.Context) {
	token, ok := bearerToken(c.GetHeader("Authorization"))
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "缺少访问令牌"})
		return
	}
	principal, err := h.tokens.VerifyAccessToken(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "访问令牌无效"})
		return
	}
	view, err := h.service.InspectApp(c.Request.Context(), principal, c.Param("appId"))
	if err != nil {
		writeAppRuntimeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"runtime": view})
}

// Initialize 触发应用初始化重试。
// 仅当 status ∈ {error, draft} 允许；其它状态返回 409。
func (h *AppRuntimeHandler) Initialize(c *gin.Context) {
	token, ok := bearerToken(c.GetHeader("Authorization"))
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "缺少访问令牌"})
		return
	}
	principal, err := h.tokens.VerifyAccessToken(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "访问令牌无效"})
		return
	}
	result, err := h.service.RequestInitialize(c.Request.Context(), principal, c.Param("appId"))
	if err != nil {
		writeAppRuntimeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"runtime_operation": result})
}

func (h *AppRuntimeHandler) trigger(c *gin.Context, op service.RuntimeOperation) {
	token, ok := bearerToken(c.GetHeader("Authorization"))
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "缺少访问令牌"})
		return
	}
	principal, err := h.tokens.VerifyAccessToken(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "访问令牌无效"})
		return
	}
	result, err := h.service.Trigger(c.Request.Context(), principal, c.Param("appId"), op)
	if err != nil {
		writeAppRuntimeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"runtime_operation": result})
}

func writeAppRuntimeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrRuntimeOperationDenied), errors.Is(err, service.ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "无权执行该运行操作"})
	case errors.Is(err, service.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "应用不存在"})
	case errors.Is(err, service.ErrAppNotReinitializable):
		c.JSON(http.StatusConflict, gin.H{"error": "应用当前状态不允许重新初始化"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "运行操作暂不可用"})
	}
}
