package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	redactlog "oc-manager/internal/log"
	"oc-manager/internal/service"
)

// MembersHandler 处理组织成员相关 HTTP 路由。
// handler 只做请求绑定与状态码映射，业务规则统一在 service 层。
type MembersHandler struct {
	service    memberService
	onboarding onboardingService
	tokens     *auth.TokenManager
	// jobNotifier 用于在 DeleteMember 联动 app_delete 时即时入队 Redis；
	// 缺失时 service 的删除依然会写库，仅入队步骤被跳过。
	jobNotifier service.JobNotifier
}

type memberService interface {
	CreateMember(ctx context.Context, principal auth.Principal, orgID string, input service.MemberInput) (service.MemberResult, error)
	ListMembers(ctx context.Context, principal auth.Principal, orgID string, limit, offset int32) ([]service.MemberResult, error)
	GetMember(ctx context.Context, principal auth.Principal, userID string) (service.MemberResult, error)
	UpdateMemberProfile(ctx context.Context, principal auth.Principal, userID string, input service.MemberInput) (service.MemberResult, error)
	SetMemberStatus(ctx context.Context, principal auth.Principal, userID, status string) (service.MemberResult, error)
	ResetMemberPassword(ctx context.Context, principal auth.Principal, userID, newPassword string) error
	DeleteMember(ctx context.Context, principal auth.Principal, userID string, notifier service.JobNotifier) error
}

type onboardingService interface {
	OnboardMember(ctx context.Context, principal auth.Principal, orgID string, input service.OnboardMemberInput) (service.OnboardMemberResult, error)
}

// NewMembersHandler 创建成员 handler。
func NewMembersHandler(service memberService, tokens *auth.TokenManager) *MembersHandler {
	return &MembersHandler{service: service, tokens: tokens}
}

// SetOnboardingService 给已存在的 handler 注入 onboarding 能力，便于按需启用。
// 如果调用方没有调用此方法，POST /onboard 路由会返回 503，避免暴露未实现的能力。
func (h *MembersHandler) SetOnboardingService(svc onboardingService) {
	h.onboarding = svc
}

// SetJobNotifier 注入 Redis job notifier，使 DeleteMember 联动 app_delete 时立即入队。
func (h *MembersHandler) SetJobNotifier(notifier service.JobNotifier) {
	h.jobNotifier = notifier
}

// RegisterMemberRoutes 注册成员路由。
// 组织维度的列表/创建挂在 /organizations/:orgId/members；
// 单条成员的查询、更新、状态切换、密码重置挂在 /members/:userId。
// 创建成员并联动初始化应用的事务路由挂在 /organizations/:orgId/members/onboard。
func RegisterMemberRoutes(router gin.IRouter, handler *MembersHandler) {
	orgGroup := router.Group("/api/v1/organizations/:orgId/members")
	orgGroup.GET("", handler.List)
	orgGroup.POST("", handler.Create)
	orgGroup.POST("/onboard", handler.Onboard)

	memberGroup := router.Group("/api/v1/members")
	memberGroup.GET("/:userId", handler.Get)
	memberGroup.PATCH("/:userId", handler.Update)
	memberGroup.POST("/:userId/disable", handler.Disable)
	memberGroup.POST("/:userId/enable", handler.Enable)
	memberGroup.POST("/:userId/password", handler.ResetPassword)
	memberGroup.DELETE("/:userId", handler.Delete)
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

// onboardMemberRequest 是创建成员并联动应用的请求体。
type onboardMemberRequest struct {
	Username    string `json:"username" binding:"required"`
	DisplayName string `json:"display_name" binding:"required"`
	Password    string `json:"password" binding:"required"`
	Role        string `json:"role"`
	AppName     string `json:"app_name" binding:"required"`
	AppPrompt   string `json:"app_prompt"`
	PersonaMode string `json:"persona_mode"`
	ChannelType string `json:"channel_type"`
	NodeID      string `json:"runtime_node_id"`
}

// Onboard 在事务中创建成员、应用、渠道绑定、审计与初始化任务。
// 调用方未配置 onboarding service 时返回 503，避免暴露未实现的行为。
func (h *MembersHandler) Onboard(c *gin.Context) {
	if h.onboarding == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "成员联动应用流程暂未启用"})
		return
	}
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	var req onboardMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数不完整"})
		return
	}
	result, err := h.onboarding.OnboardMember(c.Request.Context(), principal, c.Param("orgId"), service.OnboardMemberInput{
		Username:    req.Username,
		DisplayName: req.DisplayName,
		Password:    req.Password,
		Role:        req.Role,
		AppName:     req.AppName,
		AppPrompt:   req.AppPrompt,
		PersonaMode: req.PersonaMode,
		ChannelType: req.ChannelType,
		NodeID:      req.NodeID,
	})
	if err != nil {
		writeMemberError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"onboarding": result})
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

// Delete 软删成员并联动应用回收。
func (h *MembersHandler) Delete(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	if err := h.service.DeleteMember(c.Request.Context(), principal, c.Param("userId"), h.jobNotifier); err != nil {
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
		c.JSON(http.StatusBadRequest, gin.H{"error": redactlog.SafeErrorMessage(err)})
	case errors.Is(err, service.ErrNoNodeAvailable):
		// 自动选节点失败：当前没有 active 且剩余容量 > 0 的节点；前端需要展示明确文案让 ops 加节点或解禁。
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"code":    "NO_NODE_AVAILABLE",
			"message": "暂无可用 Runtime Node，请联系平台管理员调整节点容量或新增节点",
		})
	default:
		// 未知错误冒泡到日志，便于运维定位；响应仍保持脱敏。
		log.Printf("member handler 未识别错误: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务暂时不可用"})
	}
}
