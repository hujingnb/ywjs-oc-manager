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

type OrganizationsHandler struct {
	service organizationService
	tokens  *auth.TokenManager
}

type organizationService interface {
	CreateOrganization(ctx context.Context, principal auth.Principal, input service.OrganizationInput) (service.OrganizationResult, error)
	ListOrganizations(ctx context.Context, principal auth.Principal, limit, offset int32) ([]service.OrganizationResult, error)
	GetOrganization(ctx context.Context, principal auth.Principal, orgID string) (service.OrganizationResult, error)
	UpdateOrganization(ctx context.Context, principal auth.Principal, orgID string, input service.OrganizationInput) (service.OrganizationResult, error)
	SetOrganizationStatus(ctx context.Context, principal auth.Principal, orgID, status string) (service.OrganizationResult, error)
}

func NewOrganizationsHandler(service organizationService, tokens *auth.TokenManager) *OrganizationsHandler {
	return &OrganizationsHandler{service: service, tokens: tokens}
}

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

func toOrganizationInput(req OrganizationRequest) service.OrganizationInput {
	return service.OrganizationInput{
		Name:                   req.Name,
		ContactName:            req.ContactName,
		ContactPhone:           req.ContactPhone,
		Remark:                 req.Remark,
		CreditWarningThreshold: req.CreditWarningThreshold,
	}
}

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
