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

type organizationRequest struct {
	Name                   string `json:"name" binding:"required"`
	ContactName            string `json:"contact_name"`
	ContactPhone           string `json:"contact_phone"`
	Remark                 string `json:"remark"`
	NewAPIUserID           string `json:"newapi_user_id"`
	CreditWarningThreshold *int32 `json:"credit_warning_threshold"`
}

func (h *OrganizationsHandler) Create(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	var req organizationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数不完整"})
		return
	}
	result, err := h.service.CreateOrganization(c.Request.Context(), principal, toOrganizationInput(req))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"organization": result})
}

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

func (h *OrganizationsHandler) Update(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	var req organizationRequest
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

func (h *OrganizationsHandler) Disable(c *gin.Context) {
	h.setStatus(c, domain.StatusDisabled)
}

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

func toOrganizationInput(req organizationRequest) service.OrganizationInput {
	return service.OrganizationInput{
		Name:                   req.Name,
		ContactName:            req.ContactName,
		ContactPhone:           req.ContactPhone,
		Remark:                 req.Remark,
		NewAPIUserID:           req.NewAPIUserID,
		CreditWarningThreshold: req.CreditWarningThreshold,
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
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务暂时不可用"})
	}
}
