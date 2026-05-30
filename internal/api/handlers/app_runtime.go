package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// AppRuntimeHandler 暴露应用容器的高风险运行操作：start/stop/restart/delete。
type AppRuntimeHandler struct {
	service runtimeOperationService
}

type runtimeOperationService interface {
	Trigger(ctx context.Context, principal auth.Principal, appID string, op service.RuntimeOperation) (service.RuntimeOperationResult, error)
	RequestInitialize(ctx context.Context, principal auth.Principal, appID string) (service.RuntimeOperationResult, error)
	InspectApp(ctx context.Context, principal auth.Principal, appID string) (service.RuntimeView, error)
}

// NewAppRuntimeHandler 创建 handler。
func NewAppRuntimeHandler(svc runtimeOperationService) *AppRuntimeHandler {
	return &AppRuntimeHandler{service: svc}
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
	keyGroup := router.Group("/api/v1/apps/:appId/api-key")
	keyGroup.POST("/disable", handler.DisableAPIKey)
	keyGroup.POST("/restore", handler.RestoreAPIKey)
}

// Start 触发启动。
//
// @Summary      启动应用容器
// @Description  异步触发指定应用容器的启动操作，返回 job 引用
// @Tags         runtime-operations
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "应用 ID"
// @Success      202    {object}  map[string]service.RuntimeOperationResult
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/runtime/start [post]
func (h *AppRuntimeHandler) Start(c *gin.Context) { h.trigger(c, service.RuntimeOperationStart) }

// Stop 触发停止。
//
// @Summary      停止应用容器
// @Description  异步触发指定应用容器的停止操作，返回 job 引用
// @Tags         runtime-operations
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "应用 ID"
// @Success      202    {object}  map[string]service.RuntimeOperationResult
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/runtime/stop [post]
func (h *AppRuntimeHandler) Stop(c *gin.Context) { h.trigger(c, service.RuntimeOperationStop) }

// Restart 触发重启。
//
// @Summary      重启应用容器
// @Description  异步触发指定应用容器的重启操作，返回 job 引用
// @Tags         runtime-operations
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "应用 ID"
// @Success      202    {object}  map[string]service.RuntimeOperationResult
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/runtime/restart [post]
func (h *AppRuntimeHandler) Restart(c *gin.Context) { h.trigger(c, service.RuntimeOperationRestart) }

// Delete 触发删除。
//
// @Summary      删除应用容器
// @Description  异步触发指定应用容器的删除操作，返回 job 引用
// @Tags         runtime-operations
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "应用 ID"
// @Success      202    {object}  map[string]service.RuntimeOperationResult
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/runtime/delete [post]
func (h *AppRuntimeHandler) Delete(c *gin.Context) { h.trigger(c, service.RuntimeOperationDelete) }

// DisableAPIKey 触发禁用 new-api token；仅应用所属组织管理员。
//
// @Summary      禁用应用 API Key
// @Description  异步触发禁用应用关联的 new-api token，仅应用所属企业管理员可操作
// @Tags         runtime-operations
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "应用 ID"
// @Success      202    {object}  map[string]service.RuntimeOperationResult
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/api-key/disable [post]
func (h *AppRuntimeHandler) DisableAPIKey(c *gin.Context) {
	h.trigger(c, service.RuntimeOperationDisableAPIKey)
}

// RestoreAPIKey 触发启用 new-api token；仅应用所属组织管理员。
//
// @Summary      恢复应用 API Key
// @Description  异步触发恢复应用关联的 new-api token，仅应用所属企业管理员可操作
// @Tags         runtime-operations
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "应用 ID"
// @Success      202    {object}  map[string]service.RuntimeOperationResult
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/api-key/restore [post]
func (h *AppRuntimeHandler) RestoreAPIKey(c *gin.Context) {
	h.trigger(c, service.RuntimeOperationRestoreAPIKey)
}

// GetRuntime 返回应用 k8s 运行时视图。
// spec-A2b：Container 字段已删除，返回 status + snapshot。
//
// @Summary      查询应用运行时状态
// @Description  返回应用 k8s 运行时状态与最近快照；status=no_container 表示容器尚未创建
// @Tags         runtime-operations
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "应用 ID"
// @Success      200    {object}  map[string]service.RuntimeView
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/runtime [get]
func (h *AppRuntimeHandler) GetRuntime(c *gin.Context) {
	principal := principalFromCtx(c)
	// principal 由中间件注入；容器可见性由 service 再按 app owner/org 校验。
	view, err := h.service.InspectApp(c.Request.Context(), principal, c.Param("appId"))
	if err != nil {
		writeAppRuntimeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"runtime": view})
}

// Initialize 触发应用初始化重试。
// 仅当 status ∈ {error, draft} 允许；其它状态返回 409。
//
// @Summary      重新初始化应用
// @Description  触发应用初始化重试；仅当应用状态为 error 或 draft 时允许，其它状态返回 409
// @Tags         runtime-operations
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "应用 ID"
// @Success      202    {object}  map[string]service.RuntimeOperationResult
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      409    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/initialize [post]
func (h *AppRuntimeHandler) Initialize(c *gin.Context) {
	principal := principalFromCtx(c)
	// 重新初始化属于写操作，状态机和权限边界由 RuntimeOperationService 统一判断。
	result, err := h.service.RequestInitialize(c.Request.Context(), principal, c.Param("appId"))
	if err != nil {
		writeAppRuntimeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"runtime_operation": result})
}

// trigger 由 RequireUserAuth 中间件注入 principal，派发高风险 runtime 操作。
// handler 不直接判断角色或应用归属，避免与 service 层 authorizer 规则分叉。
func (h *AppRuntimeHandler) trigger(c *gin.Context, op service.RuntimeOperation) {
	principal := principalFromCtx(c)
	result, err := h.service.Trigger(c.Request.Context(), principal, c.Param("appId"), op)
	if err != nil {
		writeAppRuntimeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"runtime_operation": result})
}

// writeAppRuntimeError 将运行操作 service 的错误映射为 HTTP 状态码。
// ErrAppNotReinitializable 单独映射 409，便于前端区分状态冲突和权限失败。
func writeAppRuntimeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrRuntimeOperationDenied), errors.Is(err, service.ErrForbidden):
		c.JSON(http.StatusForbidden, apierror.New("RUNTIME_OP_FORBIDDEN", "无权执行该运行操作"))
	case errors.Is(err, service.ErrNotFound):
		c.JSON(http.StatusNotFound, apierror.New("NOT_FOUND", "应用不存在"))
	case errors.Is(err, service.ErrAppNotReinitializable):
		c.JSON(http.StatusConflict, apierror.New("APP_NOT_REINIT", "应用当前状态不允许重新初始化"))
	default:
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "运行操作暂不可用"))
	}
}
