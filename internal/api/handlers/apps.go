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

// AppsHandler 暴露应用读取和轻量配置接口；实例创建仍位于 onboarding handler。
//
// 路由挂在 user 组上，token 校验由 RequireUserAuth 中间件统一完成。
type AppsHandler struct {
	service appService
}

type appService interface {
	Get(ctx context.Context, principal auth.Principal, appID string) (service.AppResult, error)
	ListByOrg(ctx context.Context, principal auth.Principal, orgID string, limit, offset int32) ([]service.AppResult, error)
	SwitchAppVersion(ctx context.Context, principal auth.Principal, appID, versionID string) (service.AppResult, error)
	UpdateAppKnowledgeQuota(ctx context.Context, principal auth.Principal, appID string, quotaBytes int64) (service.AppResult, error)
	UpdateAppLocale(ctx context.Context, principal auth.Principal, appID, locale string) (service.AppResult, error)
}

// NewAppsHandler 创建 handler。
func NewAppsHandler(svc appService) *AppsHandler {
	return &AppsHandler{service: svc}
}

// RegisterAppRoutes 注册应用路由。
// 列表挂在组织维度 /organizations/:orgId/apps；详情、版本切换和容量配置挂在 /apps/:appId。
func RegisterAppRoutes(router gin.IRouter, handler *AppsHandler) {
	router.GET("/api/v1/organizations/:orgId/apps", handler.List)
	router.GET("/api/v1/apps/:appId", handler.Get)
	router.POST("/api/v1/apps/:appId/version", handler.SwitchVersion)
	router.PATCH("/api/v1/apps/:appId/knowledge/quota", handler.UpdateKnowledgeQuota)
	router.PATCH("/api/v1/apps/:appId/locale", handler.UpdateLocale)
}

// List 列出组织内的应用。
//
// @Summary      应用列表
// @Description  按企业 ID 分页列出应用；org_member 只能看到自己的应用
// @Tags         apps
// @Produce      json
// @Security     BearerAuth
// @Param        orgId   path      string  true   "企业 ID"
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

// SwitchVersion 切换实例绑定的助手版本。
//
// @Summary      切换实例助手版本
// @Description  切换实例绑定的助手版本；目标版本必须在实例所属企业的 allowlist 内。切换后需重启实例生效。
// @Tags         apps
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string                  true  "应用 ID"
// @Param        body   body      SwitchAppVersionRequest true  "目标版本"
// @Success      200    {object}  map[string]service.AppResult
// @Failure      400    {object}  ErrorResponse
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/version [post]
func (h *AppsHandler) SwitchVersion(c *gin.Context) {
	var req SwitchAppVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "请求体格式错误"))
		return
	}
	principal := principalFromCtx(c)
	result, err := h.service.SwitchAppVersion(c.Request.Context(), principal, c.Param("appId"), req.VersionID)
	if err != nil {
		writeAppsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"app": result})
}

// UpdateKnowledgeQuota 更新实例知识库容量上限。
//
// @Summary      更新实例知识库容量
// @Description  更新单个实例知识库累计容量上限，允许低于当前已用，后续上传会被拦截
// @Tags         apps
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string                         true  "应用 ID"
// @Param        body   body      UpdateAppKnowledgeQuotaRequest true  "容量上限"
// @Success      200    {object}  map[string]service.AppResult
// @Failure      400    {object}  ErrorResponse
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/knowledge/quota [patch]
func (h *AppsHandler) UpdateKnowledgeQuota(c *gin.Context) {
	var req UpdateAppKnowledgeQuotaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "请求体格式错误"))
		return
	}
	principal := principalFromCtx(c)
	result, err := h.service.UpdateAppKnowledgeQuota(c.Request.Context(), principal, c.Param("appId"), req.QuotaBytes)
	if err != nil {
		writeAppsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"app": result})
}

// UpdateLocale 修改实例语言（hermes bot 对终端用户说话的语言）。
//
// @Summary      更新实例语言
// @Description  更新实例 hermes bot 对终端用户说话的语言（en/zh），持久化后触发容器重启使配置生效
// @Tags         apps
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string                  true  "应用 ID"
// @Param        body   body      UpdateAppLocaleRequest  true  "目标语言"
// @Success      200    {object}  map[string]service.AppResult
// @Failure      400    {object}  ErrorResponse
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/locale [patch]
func (h *AppsHandler) UpdateLocale(c *gin.Context) {
	var req UpdateAppLocaleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "请求体格式错误"))
		return
	}
	principal := principalFromCtx(c)
	result, err := h.service.UpdateAppLocale(c.Request.Context(), principal, c.Param("appId"), req.Locale)
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
	case errors.Is(err, service.ErrVersionNotInAllowlist):
		c.JSON(http.StatusBadRequest, apierror.New("VERSION_NOT_ALLOWED", "助手版本不在企业允许列表内"))
	case errors.Is(err, service.ErrInvalidLocale):
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_LOCALE", "不支持的语言，请使用 en 或 zh"))
	default:
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "服务暂时不可用"))
	}
}
