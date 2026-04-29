package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// MembersHandler 处理组织成员相关 HTTP 路由。
// handler 只做请求绑定与状态码映射，业务规则统一在 service 层。
type MembersHandler struct {
	service memberService
	tokens  *auth.TokenManager
}

type memberService interface {
	CreateMember(ctx context.Context, principal auth.Principal, orgID string, input service.MemberInput) (service.MemberResult, error)
	ListMembers(ctx context.Context, principal auth.Principal, orgID string, limit, offset int32) ([]service.MemberResult, error)
	GetMember(ctx context.Context, principal auth.Principal, userID string) (service.MemberResult, error)
	UpdateMemberProfile(ctx context.Context, principal auth.Principal, userID string, input service.MemberInput) (service.MemberResult, error)
	SetMemberStatus(ctx context.Context, principal auth.Principal, userID, status string) (service.MemberResult, error)
	ResetMemberPassword(ctx context.Context, principal auth.Principal, userID, newPassword string) error
}

// NewMembersHandler 创建成员 handler。
func NewMembersHandler(service memberService, tokens *auth.TokenManager) *MembersHandler {
	return &MembersHandler{service: service, tokens: tokens}
}

// RegisterMemberRoutes 注册成员路由。
// 组织维度的列表/创建挂在 /organizations/:orgId/members；
// 单条成员的查询、更新、状态切换、密码重置挂在 /members/:userId。
func RegisterMemberRoutes(router gin.IRouter, handler *MembersHandler) {
	orgGroup := router.Group("/api/v1/organizations/:orgId/members")
	orgGroup.GET("", handler.List)
	orgGroup.POST("", handler.Create)

	memberGroup := router.Group("/api/v1/members")
	memberGroup.GET("/:userId", handler.Get)
	memberGroup.PATCH("/:userId", handler.Update)
	memberGroup.POST("/:userId/disable", handler.Disable)
	memberGroup.POST("/:userId/enable", handler.Enable)
	memberGroup.POST("/:userId/password", handler.ResetPassword)
}

type createMemberRequest struct {
	Username    string `json:"username" binding:"required"`
	DisplayName string `json:"display_name" binding:"required"`
	Password    string `json:"password" binding:"required"`
	Role        string `json:"role"`
}

type updateMemberRequest struct {
	DisplayName string `json:"display_name" binding:"required"`
	Role        string `json:"role"`
}

type resetPasswordRequest struct {
	Password string `json:"password" binding:"required"`
}

// Create 处理创建成员。
func (h *MembersHandler) Create(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	var req createMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数不完整"})
		return
	}
	result, err := h.service.CreateMember(c.Request.Context(), principal, c.Param("orgId"), service.MemberInput{
		Username:    req.Username,
		DisplayName: req.DisplayName,
		Password:    req.Password,
		Role:        req.Role,
	})
	if err != nil {
		writeMemberError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"member": result})
}

// List 处理成员列表。
func (h *MembersHandler) List(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	limit := queryInt32(c, "limit", 0)
	offset := queryInt32(c, "offset", 0)
	results, err := h.service.ListMembers(c.Request.Context(), principal, c.Param("orgId"), limit, offset)
	if err != nil {
		writeMemberError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"members": results})
}

// Get 查询单个成员明细。
func (h *MembersHandler) Get(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	result, err := h.service.GetMember(c.Request.Context(), principal, c.Param("userId"))
	if err != nil {
		writeMemberError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"member": result})
}

// Update 修改成员显示名/角色。
func (h *MembersHandler) Update(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	var req updateMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数不完整"})
		return
	}
	result, err := h.service.UpdateMemberProfile(c.Request.Context(), principal, c.Param("userId"), service.MemberInput{
		DisplayName: req.DisplayName,
		Role:        req.Role,
	})
	if err != nil {
		writeMemberError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"member": result})
}

// Disable 禁用成员。
func (h *MembersHandler) Disable(c *gin.Context) {
	h.setStatus(c, domain.StatusDisabled)
}

// Enable 启用成员。
func (h *MembersHandler) Enable(c *gin.Context) {
	h.setStatus(c, domain.StatusActive)
}

func (h *MembersHandler) setStatus(c *gin.Context, status string) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	result, err := h.service.SetMemberStatus(c.Request.Context(), principal, c.Param("userId"), status)
	if err != nil {
		writeMemberError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"member": result})
}

// ResetPassword 由管理员重置成员密码。
func (h *MembersHandler) ResetPassword(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	var req resetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数不完整"})
		return
	}
	if err := h.service.ResetMemberPassword(c.Request.Context(), principal, c.Param("userId"), req.Password); err != nil {
		writeMemberError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *MembersHandler) principal(c *gin.Context) (auth.Principal, bool) {
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

func writeMemberError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "无权执行该操作"})
	case errors.Is(err, service.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "资源不存在"})
	case errors.Is(err, service.ErrMemberCreateInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务暂时不可用"})
	}
}
