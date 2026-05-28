package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

const usageDefaultWindowSeconds int64 = 30 * 24 * 60 * 60

// UsageHandler 处理用量查询。
//
// 路由 / 响应字段已切换为「直接代理 new-api」语义：
//   - app / member 维度：返回 LogsPage（new-api `GET /api/log/?token_name=` 的薄包装）；
//   - organization / platform 维度：返回 QuotaSeries（new-api `GET /api/data/...` 的薄包装）。
type UsageHandler struct {
	service usageService
}

type usageService interface {
	GetAppUsage(ctx context.Context, principal auth.Principal, appID, ownerOrgID, ownerUserID string, newapiKeyID int64, opts service.LogsQueryOptions) (service.LogsPage, error)
	GetMemberUsage(ctx context.Context, principal auth.Principal, orgID, memberID string, opts service.LogsQueryOptions) (service.LogsPage, error)
	GetOrgUsage(ctx context.Context, principal auth.Principal, orgID string, since, until int64) (service.QuotaSeries, error)
	GetPlatformUsage(ctx context.Context, principal auth.Principal, since, until int64) (service.QuotaSeries, error)
	GetOrgUsageBreakdown(ctx context.Context, principal auth.Principal, since, until int64) (service.OrgUsageBreakdown, error)
}

// NewUsageHandler 创建 usage handler。
func NewUsageHandler(svc usageService) *UsageHandler {
	return &UsageHandler{service: svc}
}

// RegisterUsageRoutes 注册用量路由。
// 调用方需通过 query 参数提供 owner_org_id/owner_user_id/newapi_key_id，
// 因为 service 不直接读取 sqlc 类型。
func RegisterUsageRoutes(router gin.IRouter, handler *UsageHandler) {
	router.GET("/api/v1/apps/:appId/usage", handler.GetApp)
	router.GET("/api/v1/usage/members/:userId", handler.GetMember)
	router.GET("/api/v1/usage/organizations/:orgId", handler.GetOrg)
	router.GET("/api/v1/usage/platform", handler.GetPlatform)
	router.GET("/api/v1/platform/usage/org-breakdown", handler.GetOrgBreakdown)
}

// GetMember 返回单个成员名下应用的调用日志。
//
// @Summary      成员用量日志
// @Description  返回指定成员名下应用的 token 调用日志（LogsPage 格式）
// @Tags         usage
// @Produce      json
// @Security     BearerAuth
// @Param        userId     path      string  true   "成员用户 ID"
// @Param        org_id     query     string  true   "成员所属企业 ID"
// @Param        since      query     int     false  "起始时间（Unix 秒）"
// @Param        until      query     int     false  "结束时间（Unix 秒）"
// @Param        page       query     int     false  "页码"
// @Param        page_size  query     int     false  "每页条数"
// @Param        model_name query     string  false  "按模型名过滤"
// @Success      200        {object}  map[string]service.LogsPage
// @Failure      400        {object}  ErrorResponse
// @Failure      401        {object}  ErrorResponse
// @Failure      403        {object}  ErrorResponse
// @Failure      404        {object}  ErrorResponse
// @Failure      500        {object}  ErrorResponse
// @Failure      503        {object}  ErrorResponse
// @Router       /usage/members/{userId} [get]
func (h *UsageHandler) GetMember(c *gin.Context) {
	principal := principalFromCtx(c)
	orgID := c.Query("org_id")
	if orgID == "" {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "缺少 org_id"))
		return
	}
	view, err := h.service.GetMemberUsage(c.Request.Context(), principal, orgID, c.Param("userId"), parseLogsQueryOptions(c))
	if err != nil {
		writeUsageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"usage": view})
}

// GetOrg 返回组织维度的按日 quota。
//
// @Summary      企业用量统计
// @Description  返回指定企业在时间窗口内的按日 quota 消耗（QuotaSeries 格式）
// @Tags         usage
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path      string  true   "企业 ID"
// @Param        since  query     int     false  "起始时间（Unix 秒）"
// @Param        until  query     int     false  "结束时间（Unix 秒）"
// @Success      200    {object}  map[string]service.QuotaSeries
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /usage/organizations/{orgId} [get]
func (h *UsageHandler) GetOrg(c *gin.Context) {
	principal := principalFromCtx(c)
	since, until := parseUsageStatsWindow(c)
	view, err := h.service.GetOrgUsage(c.Request.Context(), principal, c.Param("orgId"), since, until)
	if err != nil {
		writeUsageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"usage": view})
}

// GetPlatform 返回平台维度的按日 quota（跨所有组织）。仅平台管理员可调。
//
// @Summary      平台用量统计
// @Description  返回平台维度在时间窗口内的按日 quota 消耗（跨所有企业），仅平台管理员可调
// @Tags         usage
// @Produce      json
// @Security     BearerAuth
// @Param        since  query     int     false  "起始时间（Unix 秒）"
// @Param        until  query     int     false  "结束时间（Unix 秒）"
// @Success      200    {object}  map[string]service.QuotaSeries
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /usage/platform [get]
func (h *UsageHandler) GetPlatform(c *gin.Context) {
	principal := principalFromCtx(c)
	since, until := parseUsageStatsWindow(c)
	view, err := h.service.GetPlatformUsage(c.Request.Context(), principal, since, until)
	if err != nil {
		writeUsageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"usage": view})
}

// GetOrgBreakdown 返回各组织近期 quota 消耗的 top 10 汇总，供平台控制台图表使用。
// 仅 platform_admin 可调；service 层再做一次角色校验。
//
// @Summary      各企业用量分布
// @Description  平台维度各企业在时间窗口内的 quota 消耗 top 10，仅平台管理员可调
// @Tags         usage
// @Produce      json
// @Security     BearerAuth
// @Param        since  query     int     false  "起始时间（Unix 秒）"
// @Param        until  query     int     false  "结束时间（Unix 秒）"
// @Success      200    {object}  map[string]service.OrgUsageBreakdown
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /platform/usage/org-breakdown [get]
func (h *UsageHandler) GetOrgBreakdown(c *gin.Context) {
	principal := principalFromCtx(c)
	since, until := parseUsageStatsWindow(c)
	view, err := h.service.GetOrgUsageBreakdown(c.Request.Context(), principal, since, until)
	if err != nil {
		writeUsageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"breakdown": view})
}

// GetApp 拉取应用维度的 token 调用日志。
//
// @Summary      应用用量日志
// @Description  返回指定应用的 token 调用日志（LogsPage 格式）；需同时提供 owner_org_id 和 owner_user_id
// @Tags         usage
// @Produce      json
// @Security     BearerAuth
// @Param        appId         path      string  true   "应用 ID"
// @Param        owner_org_id  query     string  true   "应用所属企业 ID"
// @Param        owner_user_id query     string  true   "应用所有者用户 ID"
// @Param        newapi_key_id query     int     false  "new-api token key ID"
// @Param        since         query     int     false  "起始时间（Unix 秒）"
// @Param        until         query     int     false  "结束时间（Unix 秒）"
// @Param        page          query     int     false  "页码"
// @Param        page_size     query     int     false  "每页条数"
// @Param        model_name    query     string  false  "按模型名过滤"
// @Success      200           {object}  map[string]service.LogsPage
// @Failure      400           {object}  ErrorResponse
// @Failure      401           {object}  ErrorResponse
// @Failure      403           {object}  ErrorResponse
// @Failure      404           {object}  ErrorResponse
// @Failure      500           {object}  ErrorResponse
// @Failure      503           {object}  ErrorResponse
// @Router       /apps/{appId}/usage [get]
func (h *UsageHandler) GetApp(c *gin.Context) {
	principal := principalFromCtx(c)
	orgID := c.Query("owner_org_id")
	owner := c.Query("owner_user_id")
	if orgID == "" || owner == "" {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "缺少 owner_org_id 或 owner_user_id"))
		return
	}
	keyID, _ := strconv.ParseInt(c.Query("newapi_key_id"), 10, 64)
	view, err := h.service.GetAppUsage(c.Request.Context(), principal, c.Param("appId"), orgID, owner, keyID, parseLogsQueryOptions(c))
	if err != nil {
		writeUsageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"usage": view})
}

// parseLogsQueryOptions 解析常用 logs 端点的 query string。
func parseLogsQueryOptions(c *gin.Context) service.LogsQueryOptions {
	since, until := parseTimeWindow(c)
	page, _ := strconv.Atoi(c.Query("page"))
	pageSize, _ := strconv.Atoi(c.Query("page_size"))
	return service.LogsQueryOptions{
		Since:     since,
		Until:     until,
		Page:      page,
		PageSize:  pageSize,
		ModelName: c.Query("model_name"),
	}
}

// parseTimeWindow 解析 since / until 两个 unix 秒 query 参数；缺失返回 0（service 层不限制）。
func parseTimeWindow(c *gin.Context) (int64, int64) {
	since, _ := strconv.ParseInt(c.Query("since"), 10, 64)
	until, _ := strconv.ParseInt(c.Query("until"), 10, 64)
	return since, until
}

// parseUsageStatsWindow 解析组织 / 平台统计时间窗；未显式传参时默认查最近 30 天，
// 避免上游 new-api 在空时间窗语义下返回空统计。
func parseUsageStatsWindow(c *gin.Context) (int64, int64) {
	since, until := parseTimeWindow(c)
	if since > 0 || until > 0 {
		return since, until
	}
	now := time.Now().Unix()
	return now - usageDefaultWindowSeconds, now
}

func writeUsageError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrForbidden):
		c.JSON(http.StatusForbidden, apierror.New("FORBIDDEN", "无权访问该用量"))
	case errors.Is(err, service.ErrNotFound):
		c.JSON(http.StatusNotFound, apierror.New("NOT_FOUND", "资源不存在"))
	case errors.Is(err, service.ErrUsageUnavailable):
		c.JSON(http.StatusServiceUnavailable, apierror.New("USAGE_UNAVAILABLE", "用量服务暂不可用"))
	default:
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "用量服务异常"))
	}
}
