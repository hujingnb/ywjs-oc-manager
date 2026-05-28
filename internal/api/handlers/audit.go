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

// AuditHandler 处理审计日志查询。
// 平台和组织维度走同一个 service，权限差异由 service 层判断。
type AuditHandler struct {
	service auditService
}

type auditService interface {
	ListByOrg(ctx context.Context, principal auth.Principal, orgID string, limit, offset int32) ([]service.AuditResult, error)
	ListByTarget(ctx context.Context, principal auth.Principal, targetType, targetID string, limit, offset int32) ([]service.AuditResult, error)
}

// NewAuditHandler 创建审计 handler。
func NewAuditHandler(service auditService) *AuditHandler {
	return &AuditHandler{service: service}
}

// RegisterAuditRoutes 注册审计路由。
func RegisterAuditRoutes(router gin.IRouter, handler *AuditHandler) {
	orgGroup := router.Group("/api/v1/organizations/:orgId/audit-logs")
	orgGroup.GET("", handler.ListByOrg)

	targetGroup := router.Group("/api/v1/audit-logs")
	targetGroup.GET("", handler.ListByTarget)
}

// ListByOrg 列出组织维度的审计日志。
//
// @Summary      企业审计日志列表
// @Description  分页列出指定企业的审计日志；仅企业管理员或平台管理员可调
// @Tags         audit-logs
// @Produce      json
// @Security     BearerAuth
// @Param        orgId   path      string  true   "企业 ID"
// @Param        limit   query     int     false  "每页条数（默认不限）"
// @Param        offset  query     int     false  "分页偏移（默认 0）"
// @Success      200     {object}  map[string][]service.AuditResult
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /organizations/{orgId}/audit-logs [get]
func (h *AuditHandler) ListByOrg(c *gin.Context) {
	principal := principalFromCtx(c)
	limit := queryInt32(c, "limit", 0)
	offset := queryInt32(c, "offset", 0)
	results, err := h.service.ListByOrg(c.Request.Context(), principal, c.Param("orgId"), limit, offset)
	if err != nil {
		writeAuditError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"audit_logs": results})
}

// ListByTarget 通过 query 参数 target_type/target_id 列出资源维度审计日志。
//
// @Summary      资源维度审计日志列表
// @Description  通过 target_type 和 target_id query 参数查询指定资源的审计日志；企业成员仅可查询自己拥有的 app 审计
// @Tags         audit-logs
// @Produce      json
// @Security     BearerAuth
// @Param        target_type  query     string  true   "资源类型（如 app / member）"
// @Param        target_id    query     string  true   "资源 ID"
// @Param        limit        query     int     false  "每页条数（默认不限）"
// @Param        offset       query     int     false  "分页偏移（默认 0）"
// @Success      200          {object}  map[string][]service.AuditResult
// @Failure      400          {object}  ErrorResponse
// @Failure      401          {object}  ErrorResponse
// @Failure      403          {object}  ErrorResponse
// @Failure      404          {object}  ErrorResponse
// @Failure      500          {object}  ErrorResponse
// @Router       /audit-logs [get]
func (h *AuditHandler) ListByTarget(c *gin.Context) {
	principal := principalFromCtx(c)
	targetType := c.Query("target_type")
	targetID := c.Query("target_id")
	if targetType == "" || targetID == "" {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "缺少 target_type 或 target_id"))
		return
	}
	limit := queryInt32(c, "limit", 0)
	offset := queryInt32(c, "offset", 0)
	results, err := h.service.ListByTarget(c.Request.Context(), principal, targetType, targetID, limit, offset)
	if err != nil {
		writeAuditError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"audit_logs": results})
}

func writeAuditError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrForbidden):
		c.JSON(http.StatusForbidden, apierror.New("FORBIDDEN", "无权执行该操作"))
	case errors.Is(err, service.ErrNotFound):
		c.JSON(http.StatusNotFound, apierror.New("NOT_FOUND", "资源不存在"))
	default:
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "服务暂时不可用"))
	}
}
