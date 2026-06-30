// Package handlers 的 web_publish_sites.go 实现管理面站点管理与证书状态 HTTP 接口。
// 五条路由覆盖：
//   - 按企业列出已发布站点（组织管理员 + 平台管理员）；
//   - 手动下线指定站点（组织管理员 + 平台管理员）；
//   - 手动续期指定站点（组织管理员 + 平台管理员）；
//   - 查询企业 web-publish 配置 + 证书状态脱敏视图（任何有 CanViewOrg 权限的主体）；
//   - 平台管理员手动重试签发/续签证书。
//
// 所有权限校验均在 service 层完成，handler 只负责 HTTP 协议层。
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

// webPublishSiteService 是 WebPublishSitesHandler 依赖的站点管理最小接口，便于 handler 单测注入 stub。
type webPublishSiteService interface {
	// ListByOrg 返回企业下所有已发布站点列表；CanViewOrg 权限。
	ListByOrg(ctx context.Context, p auth.Principal, orgID string) ([]service.SiteResult, error)
	// Takedown 将站点置为 disabled 并删除整站对象；CanManageOrg 权限。
	Takedown(ctx context.Context, p auth.Principal, siteID string) error
	// Renew 延后站点过期时间并置回 active；CanManageOrg 权限。
	Renew(ctx context.Context, p auth.Principal, siteID string) error
}

// webPublishConfigReadService 是 WebPublishSitesHandler 依赖的配置读/重试最小接口，
// 与 WebPublishConfigHandler 的 webPublishConfigService 正交，避免引入多余方法。
type webPublishConfigReadService interface {
	// Get 返回企业 web-publish 配置脱敏视图；CanViewOrg 权限。
	Get(ctx context.Context, p auth.Principal, orgID string) (service.WebPublishConfigResult, error)
	// RetryProvision 平台管理员手动重试 provisioning job；CanManageWebPublishConfig 权限。
	RetryProvision(ctx context.Context, p auth.Principal, orgID string) error
}

// WebPublishSitesHandler 处理站点管理与证书状态 HTTP 路由。
// 依赖两个最小接口，便于各自独立注入 stub 进行单测。
type WebPublishSitesHandler struct {
	sites  webPublishSiteService
	config webPublishConfigReadService
}

// NewWebPublishSitesHandler 创建 WebPublishSitesHandler。
func NewWebPublishSitesHandler(sites webPublishSiteService, config webPublishConfigReadService) *WebPublishSitesHandler {
	return &WebPublishSitesHandler{sites: sites, config: config}
}

// RegisterWebPublishSiteRoutes 注册站点管理与证书状态路由。
// 路由布局：
//   - GET    /api/v1/organizations/:orgId/published-sites  → ListByOrg
//   - POST   /api/v1/published-sites/:siteId/disable       → Takedown
//   - POST   /api/v1/published-sites/:siteId/renew         → Renew
//   - GET    /api/v1/organizations/:orgId/web-publish       → GetConfig
//   - POST   /api/v1/platform/organizations/:orgId/web-publish/cert/retry → RetryProvision
func RegisterWebPublishSiteRoutes(router gin.IRouter, h *WebPublishSitesHandler) {
	// 按企业维度的站点列表与配置查询路由。
	orgGroup := router.Group("/api/v1/organizations/:orgId")
	orgGroup.GET("/published-sites", h.ListByOrg)
	orgGroup.GET("/web-publish", h.GetConfig)

	// 按站点 ID 的操作路由。
	siteGroup := router.Group("/api/v1/published-sites/:siteId")
	siteGroup.POST("/disable", h.Takedown)
	siteGroup.POST("/renew", h.Renew)

	// 平台管理员手动重试签发/续签路由。
	router.POST("/api/v1/platform/organizations/:orgId/web-publish/cert/retry", h.RetryProvision)
}

// ListByOrg 列出企业下所有已发布站点。
//
// @Summary      企业站点列表
// @Description  按企业 ID 列出所有已发布站点（active/disabled/expired），需 CanViewOrg 权限
// @Tags         web-publish
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path      string  true  "企业 ID"
// @Success      200    {object}  map[string][]service.SiteResult
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /organizations/{orgId}/published-sites [get]
func (h *WebPublishSitesHandler) ListByOrg(c *gin.Context) {
	principal := principalFromCtx(c)
	results, err := h.sites.ListByOrg(c.Request.Context(), principal, c.Param("orgId"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"sites": results})
}

// Takedown 手动下线指定站点。
//
// @Summary      手动下线站点
// @Description  将站点状态置为 disabled，并删除整站对象存储前缀；需 CanManageOrg 权限
// @Tags         web-publish
// @Security     BearerAuth
// @Param        siteId  path  string  true  "站点 ID"
// @Success      204
// @Failure      401  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /published-sites/{siteId}/disable [post]
func (h *WebPublishSitesHandler) Takedown(c *gin.Context) {
	principal := principalFromCtx(c)
	if err := h.sites.Takedown(c.Request.Context(), principal, c.Param("siteId")); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Renew 手动续期指定站点。
//
// @Summary      手动续期站点
// @Description  按企业 site_ttl_days 延后站点过期时间并置回 active；需 CanManageOrg 权限
// @Tags         web-publish
// @Security     BearerAuth
// @Param        siteId  path  string  true  "站点 ID"
// @Success      204
// @Failure      401  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /published-sites/{siteId}/renew [post]
func (h *WebPublishSitesHandler) Renew(c *gin.Context) {
	principal := principalFromCtx(c)
	if err := h.sites.Renew(c.Request.Context(), principal, c.Param("siteId")); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// GetConfig 查询企业 web-publish 配置与证书状态脱敏视图。
//
// @Summary      查询企业 web-publish 配置
// @Description  返回企业 web-publish 配置及证书状态（凭证密文不出现在响应中）；需 CanViewOrg 权限
// @Tags         web-publish
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path      string  true  "企业 ID"
// @Success      200    {object}  service.WebPublishConfigResult
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /organizations/{orgId}/web-publish [get]
func (h *WebPublishSitesHandler) GetConfig(c *gin.Context) {
	principal := principalFromCtx(c)
	result, err := h.config.Get(c.Request.Context(), principal, c.Param("orgId"))
	if err != nil {
		writeConfigServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// RetryProvision 平台管理员手动重试 provisioning（签发/续签证书）。
//
// @Summary      手动重试证书签发
// @Description  平台管理员触发手动重试 web-publish provisioning job（适用于证书签发/续签失败场景）
// @Tags         web-publish
// @Security     BearerAuth
// @Param        orgId  path  string  true  "企业 ID"
// @Success      204
// @Failure      401  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /platform/organizations/{orgId}/web-publish/cert/retry [post]
func (h *WebPublishSitesHandler) RetryProvision(c *gin.Context) {
	principal := principalFromCtx(c)
	if err := h.config.RetryProvision(c.Request.Context(), principal, c.Param("orgId")); err != nil {
		writeConfigServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// writeConfigServiceError 将 web-publish 配置/证书相关 service 错误映射为稳定 HTTP 状态码。
// 通用的 ErrForbidden/ErrNotFound 优先由 writeServiceError 覆盖；
// web-publish 特有的 ErrWebPublishNotProvisioned → 403 由 mappedServiceErrorRules 覆盖；
// 未命中任何规则时回落 500，避免暴露内部细节。
func writeConfigServiceError(c *gin.Context, err error) {
	// 先检查通用 sentinel（ErrForbidden / ErrNotFound），再走 web-publish 特有映射表。
	switch {
	case errors.Is(err, service.ErrForbidden):
		apierror.JSON(c, http.StatusForbidden, "FORBIDDEN", apierror.MsgMemberForbidden)
	case errors.Is(err, service.ErrNotFound):
		apierror.JSON(c, http.StatusNotFound, "NOT_FOUND", apierror.MsgNotFound)
	default:
		// 再尝试 mappedServiceErrorRules（含 ErrWebPublishNotProvisioned → 403）；未命中则 500。
		writeMappedServiceError(c, err, http.StatusInternalServerError, apierror.MsgWebPublishServiceUnavailable)
	}
}
