package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// OrganizationsHandler 处理组织管理 HTTP 路由。
// handler 只负责绑定 DTO、解析 principal 和映射错误码，组织权限与 new-api provisioning 在 service 层完成。
type OrganizationsHandler struct {
	service organizationService
	tokens  *auth.TokenManager
}

// organizationService 是组织 handler 依赖的最小 service 接口，便于 handler 单测注入 stub。
type organizationService interface {
	CreateOrganization(ctx context.Context, principal auth.Principal, input service.OrganizationInput) (service.OrganizationResult, error)
	ListOrganizations(ctx context.Context, principal auth.Principal, limit, offset int32) ([]service.OrganizationResult, error)
	GetOrganization(ctx context.Context, principal auth.Principal, orgID string) (service.OrganizationResult, error)
	UpdateOrganization(ctx context.Context, principal auth.Principal, orgID string, input service.OrganizationInput) (service.OrganizationResult, error)
	SetOrganizationStatus(ctx context.Context, principal auth.Principal, orgID, status string) (service.OrganizationResult, error)
}

// NewOrganizationsHandler 创建组织 handler。
func NewOrganizationsHandler(service organizationService, tokens *auth.TokenManager) *OrganizationsHandler {
	return &OrganizationsHandler{service: service, tokens: tokens}
}

// RegisterOrganizationRoutes 注册组织路由组。
// /api/v1/organizations 负责平台租户的列表、创建、资料更新和启停状态切换。
func RegisterOrganizationRoutes(router gin.IRouter, handler *OrganizationsHandler) {
	group := router.Group("/api/v1/organizations")
	group.GET("", handler.List)
	group.POST("", handler.Create)
	group.GET("/:orgId", handler.Get)
	group.PATCH("/:orgId", handler.Update)
	group.POST("/:orgId/disable", handler.Disable)
	group.POST("/:orgId/enable", handler.Enable)
}

// Create 创建组织。
//
// @Summary      创建组织
// @Description  平台管理员创建新组织，并同步在 new-api 侧完成账户 provisioning
// @Tags         organizations
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateOrganizationRequest     true  "创建组织请求"
// @Success      201   {object}  map[string]service.OrganizationResult
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      403   {object}  ErrorResponse
// @Failure      500   {object}  ErrorResponse
// @Router       /organizations [post]
func (h *OrganizationsHandler) Create(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	var req CreateOrganizationRequest
	// ShouldBindJSON 只做 HTTP 层必填字段校验；角色、new-api 和回滚规则由 service 处理。
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数不完整"})
		return
	}
	result, err := h.service.CreateOrganization(c.Request.Context(), principal, toCreateOrganizationInput(req))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"organization": result})
}

// List 列出组织列表。
//
// @Summary      组织列表
// @Description  平台管理员获取所有组织；org_admin 只能看到自己所属组织
// @Tags         organizations
// @Produce      json
// @Security     BearerAuth
// @Param        limit   query     int  false  "每页条数（默认 50）"
// @Param        offset  query     int  false  "分页偏移（默认 0）"
// @Success      200     {object}  map[string][]service.OrganizationResult
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /organizations [get]
func (h *OrganizationsHandler) List(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	limit := queryInt32(c, "limit", 50)
	offset := queryInt32(c, "offset", 0)
	results, err := h.service.ListOrganizations(c.Request.Context(), principal, limit, offset)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"organizations": results})
}

// Get 获取单个组织详情。
//
// @Summary      组织详情
// @Description  按 orgId 获取单个组织信息
// @Tags         organizations
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path      string  true  "组织 ID"
// @Success      200    {object}  map[string]service.OrganizationResult
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /organizations/{orgId} [get]
func (h *OrganizationsHandler) Get(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	result, err := h.service.GetOrganization(c.Request.Context(), principal, c.Param("orgId"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"organization": result})
}

// Update 更新组织基础信息。
//
// @Summary      更新组织
// @Description  更新组织名称、联系人、备注等基础字段
// @Tags         organizations
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path      string               true  "组织 ID"
// @Param        body   body      OrganizationRequest  true  "更新组织请求"
// @Success      200    {object}  map[string]service.OrganizationResult
// @Failure      400    {object}  ErrorResponse
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /organizations/{orgId} [patch]
func (h *OrganizationsHandler) Update(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	var req OrganizationRequest
	// OpenAPI 注解只描述对外契约，handler 仍以 binding tag 作为运行时请求体校验入口。
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数不完整"})
		return
	}
	result, err := h.service.UpdateOrganization(c.Request.Context(), principal, c.Param("orgId"), toOrganizationInput(req))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"organization": result})
}

// Disable 禁用组织。
//
// @Summary      禁用组织
// @Description  将组织状态设为 disabled，成员登录时将被拒绝
// @Tags         organizations
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path      string  true  "组织 ID"
// @Success      200    {object}  map[string]service.OrganizationResult
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /organizations/{orgId}/disable [post]
func (h *OrganizationsHandler) Disable(c *gin.Context) {
	h.setStatus(c, domain.StatusDisabled)
}

// Enable 启用组织。
//
// @Summary      启用组织
// @Description  将组织状态从 disabled 恢复为 active
// @Tags         organizations
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path      string  true  "组织 ID"
// @Success      200    {object}  map[string]service.OrganizationResult
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /organizations/{orgId}/enable [post]
func (h *OrganizationsHandler) Enable(c *gin.Context) {
	h.setStatus(c, domain.StatusActive)
}

// setStatus 复用启用/禁用的 principal 提取和错误映射逻辑。
// status 只能来自 handler 内部常量，避免客户端通过请求体传入任意状态。
func (h *OrganizationsHandler) setStatus(c *gin.Context, status string) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	result, err := h.service.SetOrganizationStatus(c.Request.Context(), principal, c.Param("orgId"), status)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"organization": result})
}

// principal 从 Bearer token 中提取调用主体。
// token 只承载认证上下文，具体组织访问权限由 service 调 authorizer.go 判断。
func (h *OrganizationsHandler) principal(c *gin.Context) (auth.Principal, bool) {
	token, ok := bearerToken(c.GetHeader("Authorization"))
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "缺少访问令牌"})
		return auth.Principal{}, false
	}
	principal, err := h.tokens.VerifyAccessToken(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "访问令牌无效"})
		return auth.Principal{}, false
	}
	return principal, true
}

// toOrganizationInput 将更新 DTO 转为 service 入参；管理员初始化字段不参与更新。
func toOrganizationInput(req OrganizationRequest) service.OrganizationInput {
	return service.OrganizationInput{
		Name:                   req.Name,
		ContactName:            req.ContactName,
		ContactPhone:           req.ContactPhone,
		Remark:                 req.Remark,
		CreditWarningThreshold: req.CreditWarningThreshold,
	}
}

// toCreateOrganizationInput 将创建 DTO 转为 service 入参，保留管理员初始化字段。
func toCreateOrganizationInput(req CreateOrganizationRequest) service.OrganizationInput {
	return service.OrganizationInput{
		Name:                   req.Name,
		ContactName:            req.ContactName,
		ContactPhone:           req.ContactPhone,
		Remark:                 req.Remark,
		CreditWarningThreshold: req.CreditWarningThreshold,
		AdminUsername:          req.AdminUsername,
		AdminDisplayName:       req.AdminDisplayName,
		AdminPassword:          req.AdminPassword,
	}
}

// queryInt32 解析分页参数；非法或缺失时使用默认值，避免 handler 将 400 暴露给旧客户端。
func queryInt32(c *gin.Context, key string, fallback int32) int32 {
	value := c.Query(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil || parsed < 0 {
		return fallback
	}
	return int32(parsed)
}

// writeServiceError 将组织 service 的 sentinel error 映射到稳定 HTTP 状态码。
// 未识别错误统一按 500 返回安全文案，避免暴露 new-api 或数据库细节。
func writeServiceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "无权执行该操作"})
	case errors.Is(err, service.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "资源不存在"})
	case errors.Is(err, service.ErrMemberCreateInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数不完整"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务暂时不可用"})
	}
}
