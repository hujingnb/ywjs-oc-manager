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

// AppsHandler 暴露应用读取接口；写操作位于 onboarding 与 runtime operation handler。
//
// 路由挂在 user 组上，token 校验由 RequireUserAuth 中间件统一完成。
type AppsHandler struct {
	service appService
}

type appService interface {
	Get(ctx context.Context, principal auth.Principal, appID string) (service.AppResult, error)
	ListByOrg(ctx context.Context, principal auth.Principal, orgID string, limit, offset int32) ([]service.AppResult, error)
}

// NewAppsHandler 创建 handler。
func NewAppsHandler(svc appService) *AppsHandler {
	return &AppsHandler{service: svc}
}

// RegisterAppRoutes 注册应用路由。
// 列表挂在组织维度 /organizations/:orgId/apps；详情挂在 /apps/:appId。
func RegisterAppRoutes(router gin.IRouter, handler *AppsHandler) {
	router.GET("/api/v1/organizations/:orgId/apps", handler.List)
	router.GET("/api/v1/apps/:appId", handler.Get)
}

// List 列出组织内的应用。
//
// @Summary      应用列表
// @Description  按组织 ID 分页列出应用；org_member 只能看到自己的应用
// @Tags         apps
// @Produce      json
// @Security     BearerAuth
// @Param        orgId   path      string  true   "组织 ID"
// @Param        limit   query     int     false  "每页条数（默认不限）"
// @Param        offset  query     int     false  "分页偏移（默认 0）"
// @Success      200     {object}  map[string][]service.AppResult
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /organizations/{orgId}/apps [get]
func (h *AppsHandler) List(c *gin.Context) {
	principal := principalFromCtx(c)
	limit := queryInt32(c, "limit", 0)
	offset := queryInt32(c, "offset", 0)
	results, err := h.service.ListByOrg(c.Request.Context(), principal, c.Param("orgId"), limit, offset)
	if err != nil {
		writeAppsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"apps": results})
}

// Get 查询单个应用详情。
//
// @Summary      应用详情
// @Description  按 appId 获取单个应用信息；org_member 只能查询自己的应用
// @Tags         apps
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "应用 ID"
// @Success      200    {object}  map[string]service.AppResult
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId} [get]
func (h *AppsHandler) Get(c *gin.Context) {
	principal := principalFromCtx(c)
	result, err := h.service.Get(c.Request.Context(), principal, c.Param("appId"))
	if err != nil {
		writeAppsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"app": result})
}

// writeAppsError 将 AppService 的 sentinel error 映射为 HTTP 状态码。
// 未识别错误统一返回 500 和安全文案，避免把数据库或 new-api 细节暴露给前端。
func writeAppsError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrForbidden):
		c.JSON(http.StatusForbidden, apierror.New("FORBIDDEN", "无权访问该应用"))
	case errors.Is(err, service.ErrNotFound):
		c.JSON(http.StatusNotFound, apierror.New("NOT_FOUND", "应用不存在"))
	case errors.Is(err, service.ErrMemberCreateInvalid):
		c.JSON(http.StatusBadRequest, apierror.New("MEMBER_INVALID", validationServiceMessage(err, service.ErrMemberCreateInvalid)))
	default:
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "服务暂时不可用"))
	}
}
