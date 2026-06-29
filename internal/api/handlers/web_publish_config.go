// Package handlers 的 web_publish_config.go 实现平台管理员配置企业 web-publish 发布能力的 HTTP 接口。
// 三个接口对应 WebPublishConfigService 的 Configure / Enable / Disable 三个方法，
// 权限校验由 service 层完成（CanManageWebPublishConfig，仅平台管理员可调用）。
package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// webPublishConfigService 是 WebPublishConfigHandler 依赖的最小 service 接口，便于 handler 单测注入 stub。
type webPublishConfigService interface {
	// Configure 写入企业 web-publish 基础配置（DNS provider、凭证加密、配额）。
	Configure(ctx context.Context, principal auth.Principal, in service.WebPublishConfigInput) error
	// Enable 开通企业 web-publish 能力（写状态机 + 派发 provisioning job）。
	Enable(ctx context.Context, principal auth.Principal, orgID string) error
	// Disable 停用企业 web-publish 能力（写状态机，不派发 job）。
	Disable(ctx context.Context, principal auth.Principal, orgID string) error
}

// WebPublishConfigHandler 处理企业 web-publish 配置 HTTP 路由。
// handler 只负责绑定 DTO、解析 principal 和映射错误码，业务权限与 provisioning 在 service 层完成。
type WebPublishConfigHandler struct {
	service webPublishConfigService
}

// NewWebPublishConfigHandler 创建 WebPublishConfigHandler。
func NewWebPublishConfigHandler(svc webPublishConfigService) *WebPublishConfigHandler {
	return &WebPublishConfigHandler{service: svc}
}

// RegisterWebPublishConfigRoutes 注册企业 web-publish 配置路由。
// 三条路由挂在 /api/v1/platform/organizations/:orgId/web-publish 下：
//   - PUT        → Configure（写 DNS 配置）
//   - POST enable → Enable（开通）
//   - POST disable → Disable（停用）
func RegisterWebPublishConfigRoutes(router gin.IRouter, h *WebPublishConfigHandler) {
	group := router.Group("/api/v1/platform/organizations/:orgId/web-publish")
	group.PUT("", h.Configure)
	group.POST("/enable", h.Enable)
	group.POST("/disable", h.Disable)
}

// Configure 写入企业 web-publish 基础配置。
//
// @Summary      配置企业发布能力
// @Description  平台管理员写入企业 web-publish 的 DNS provider、凭证（service 端加密落库）和配额参数
// @Tags         web-publish
// @Accept       json
// @Security     BearerAuth
// @Param        orgId  path  string                     true  "企业 ID"
// @Param        body   body  ConfigureWebPublishRequest  true  "配置请求"
// @Success      204
// @Failure      400  {object}  ErrorResponse
// @Failure      401  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /platform/organizations/{orgId}/web-publish [put]
func (h *WebPublishConfigHandler) Configure(c *gin.Context) {
	principal := principalFromCtx(c)
	var req ConfigureWebPublishRequest
	// ShouldBindJSON 校验 HTTP 层必填字段；DNSProvider 白名单校验在 service 层完成。
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	// 将 DTO 转为 service 入参；SiteTTLDays/MaxSites 从 int32 转为 int（service 层签名类型）。
	in := service.WebPublishConfigInput{
		OrgID:       c.Param("orgId"),
		BaseDomain:  req.BaseDomain,
		DNSProvider: req.DNSProvider,
		Credentials: req.Credentials,
		SiteTTLDays: int(req.SiteTTLDays),
		MaxSites:    int(req.MaxSites),
	}
	if err := h.service.Configure(c.Request.Context(), principal, in); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Enable 开通企业 web-publish 能力。
//
// @Summary      开通企业发布能力
// @Description  平台管理员触发企业 web-publish provisioning：写状态机为 provisioning 并派发异步 job
// @Tags         web-publish
// @Security     BearerAuth
// @Param        orgId  path  string  true  "企业 ID"
// @Success      204
// @Failure      401  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /platform/organizations/{orgId}/web-publish/enable [post]
func (h *WebPublishConfigHandler) Enable(c *gin.Context) {
	principal := principalFromCtx(c)
	if err := h.service.Enable(c.Request.Context(), principal, c.Param("orgId")); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Disable 停用企业 web-publish 能力。
//
// @Summary      停用企业发布能力
// @Description  平台管理员停用企业 web-publish：写状态机为 disabled，不派发 job，不删除配置数据
// @Tags         web-publish
// @Security     BearerAuth
// @Param        orgId  path  string  true  "企业 ID"
// @Success      204
// @Failure      401  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /platform/organizations/{orgId}/web-publish/disable [post]
func (h *WebPublishConfigHandler) Disable(c *gin.Context) {
	principal := principalFromCtx(c)
	if err := h.service.Disable(c.Request.Context(), principal, c.Param("orgId")); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
