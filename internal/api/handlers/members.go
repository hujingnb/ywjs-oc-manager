package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// MembersHandler 处理组织成员相关 HTTP 路由。
// handler 只做请求绑定与状态码映射，业务规则统一在 service 层。
type MembersHandler struct {
	service    memberService
	onboarding onboardingService
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
	CreateAppForMember(ctx context.Context, principal auth.Principal, orgID, userID string, input service.CreateAppForMemberInput) (service.CreateAppForMemberResult, error)
}

// NewMembersHandler 创建成员 handler。
func NewMembersHandler(service memberService) *MembersHandler {
	return &MembersHandler{service: service}
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
// 创建成员并联动初始化应用的事务路由挂在 /organizations/:orgId/members/onboard；
// 已有成员复建实例路由挂在 /organizations/:orgId/members/:userId/apps。
func RegisterMemberRoutes(router gin.IRouter, handler *MembersHandler) {
	orgGroup := router.Group("/api/v1/organizations/:orgId/members")
	orgGroup.GET("", handler.List)
	orgGroup.POST("", handler.Create)
	orgGroup.POST("/onboard", handler.Onboard)
	orgGroup.POST("/:userId/apps", handler.CreateAppForMember)

	memberGroup := router.Group("/api/v1/members")
	memberGroup.GET("/:userId", handler.Get)
	memberGroup.PATCH("/:userId", handler.Update)
	memberGroup.POST("/:userId/disable", handler.Disable)
	memberGroup.POST("/:userId/enable", handler.Enable)
	memberGroup.POST("/:userId/password", handler.ResetPassword)
	memberGroup.DELETE("/:userId", handler.Delete)
}

// Create 创建组织成员。
//
// @Summary      创建企业成员
// @Description  企业管理员在本企业下创建新成员
// @Tags         members
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path      string               true  "企业 ID"
// @Param        body   body      CreateMemberRequest  true  "创建成员请求"
// @Success      201    {object}  map[string]service.MemberResult
// @Failure      400    {object}  ErrorResponse
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /organizations/{orgId}/members [post]
func (h *MembersHandler) Create(c *gin.Context) {
	principal := principalFromCtx(c)
	var req CreateMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
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

// List 列出组织成员。
//
// @Summary      企业成员列表
// @Description  按企业 ID 分页列出成员；org_member 只能看到自己
// @Tags         members
// @Produce      json
// @Security     BearerAuth
// @Param        orgId   path      string  true   "企业 ID"
// @Param        limit   query     int     false  "每页条数（默认不限）"
// @Param        offset  query     int     false  "分页偏移（默认 0）"
// @Success      200     {object}  map[string][]service.MemberResult
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /organizations/{orgId}/members [get]
func (h *MembersHandler) List(c *gin.Context) {
	principal := principalFromCtx(c)
	limit := queryInt32(c, "limit", 0)
	offset := queryInt32(c, "offset", 0)
	results, err := h.service.ListMembers(c.Request.Context(), principal, c.Param("orgId"), limit, offset)
	if err != nil {
		writeMemberError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"members": results})
}

// Get 获取成员详情。
//
// @Summary      成员详情
// @Description  按 userId 获取单个成员信息；org_member 只能查询自身
// @Tags         members
// @Produce      json
// @Security     BearerAuth
// @Param        userId  path      string  true  "成员用户 ID"
// @Success      200     {object}  map[string]service.MemberResult
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /members/{userId} [get]
func (h *MembersHandler) Get(c *gin.Context) {
	principal := principalFromCtx(c)
	result, err := h.service.GetMember(c.Request.Context(), principal, c.Param("userId"))
	if err != nil {
		writeMemberError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"member": result})
}

// Update 更新成员信息。
//
// @Summary      更新成员
// @Description  更新成员显示名或角色；org_admin 仅可更新自己企业的成员
// @Tags         members
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        userId  path      string               true  "成员用户 ID"
// @Param        body    body      UpdateMemberRequest  true  "更新成员请求"
// @Success      200     {object}  map[string]service.MemberResult
// @Failure      400     {object}  ErrorResponse
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /members/{userId} [patch]
func (h *MembersHandler) Update(c *gin.Context) {
	principal := principalFromCtx(c)
	var req UpdateMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
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
//
// @Summary      禁用成员
// @Description  将成员状态设为 disabled，成员登录时将被拒绝
// @Tags         members
// @Produce      json
// @Security     BearerAuth
// @Param        userId  path      string  true  "成员用户 ID"
// @Success      200     {object}  map[string]service.MemberResult
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /members/{userId}/disable [post]
func (h *MembersHandler) Disable(c *gin.Context) {
	h.setStatus(c, domain.StatusDisabled)
}

// Enable 启用成员。
//
// @Summary      启用成员
// @Description  将成员状态从 disabled 恢复为 active
// @Tags         members
// @Produce      json
// @Security     BearerAuth
// @Param        userId  path      string  true  "成员用户 ID"
// @Success      200     {object}  map[string]service.MemberResult
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /members/{userId}/enable [post]
func (h *MembersHandler) Enable(c *gin.Context) {
	h.setStatus(c, domain.StatusActive)
}

func (h *MembersHandler) setStatus(c *gin.Context, status string) {
	principal := principalFromCtx(c)
	result, err := h.service.SetMemberStatus(c.Request.Context(), principal, c.Param("userId"), status)
	if err != nil {
		writeMemberError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"member": result})
}

// Onboard 在事务中创建成员、应用、渠道绑定、审计与初始化任务。
// 调用方未配置 onboarding service 时返回 503，避免暴露未实现的行为。
//
// @Summary      成员 onboarding（事务创建）
// @Description  在单个事务内创建成员并联动初始化应用、渠道绑定和 newapi 凭证；onboarding service 未配置时返回 503
// @Tags         members
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path      string                true  "企业 ID"
// @Param        body   body      OnboardMemberRequest  true  "onboarding 请求"
// @Success      201    {object}  map[string]service.OnboardMemberResult
// @Failure      400    {object}  ErrorResponse
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /organizations/{orgId}/members/onboard [post]
func (h *MembersHandler) Onboard(c *gin.Context) {
	if h.onboarding == nil {
		c.JSON(http.StatusServiceUnavailable, apierror.New("INTERNAL", "成员联动应用流程暂未启用"))
		return
	}
	principal := principalFromCtx(c)
	var req OnboardMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	result, err := h.onboarding.OnboardMember(c.Request.Context(), principal, c.Param("orgId"), service.OnboardMemberInput{
		Username:    req.Username,
		DisplayName: req.DisplayName,
		Password:    req.Password,
		Role:        req.Role,
		AppName:     req.AppName,
		ChannelType: req.ChannelType,
		NodeID:      req.NodeID,
		VersionID:   req.VersionID,
	})
	if err != nil {
		writeMemberError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"onboarding": result})
}

// CreateAppForMember 为已有成员创建新的应用实例。
//
// @Summary      为已有成员创建实例
// @Description  平台管理员或本企业管理员为已有成员创建新的应用实例；目标成员必须没有未删除实例
// @Tags         members
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgId   path      string                  true  "企业 ID"
// @Param        userId  path      string                  true  "成员用户 ID"
// @Param        body    body      CreateMemberAppRequest  true  "创建实例请求"
// @Success      201     {object}  map[string]service.CreateAppForMemberResult
// @Failure      400     {object}  ErrorResponse
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Failure      503     {object}  ErrorResponse
// @Router       /organizations/{orgId}/members/{userId}/apps [post]
func (h *MembersHandler) CreateAppForMember(c *gin.Context) {
	if h.onboarding == nil {
		c.JSON(http.StatusServiceUnavailable, apierror.New("INTERNAL", "成员实例创建流程暂未启用"))
		return
	}
	principal := principalFromCtx(c)
	var req CreateMemberAppRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	result, err := h.onboarding.CreateAppForMember(c.Request.Context(), principal, c.Param("orgId"), c.Param("userId"), service.CreateAppForMemberInput{
		AppName:     req.AppName,
		ChannelType: req.ChannelType,
		NodeID:      req.NodeID,
		VersionID:   req.VersionID,
	})
	if err != nil {
		writeMemberError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"member_app": result})
}

// ResetPassword 由管理员重置成员密码。
//
// @Summary      重置成员密码
// @Description  企业管理员强制重置本企业成员的密码
// @Tags         members
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        userId  path      string               true  "成员用户 ID"
// @Param        body    body      ResetPasswordRequest  true  "重置密码请求"
// @Success      204     "密码重置成功，无响应体"
// @Failure      400     {object}  ErrorResponse
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /members/{userId}/password [post]
func (h *MembersHandler) ResetPassword(c *gin.Context) {
	principal := principalFromCtx(c)
	var req ResetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	if err := h.service.ResetMemberPassword(c.Request.Context(), principal, c.Param("userId"), req.Password); err != nil {
		writeMemberError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Delete 软删成员并联动应用回收。
//
// @Summary      删除成员
// @Description  软删指定成员，并联动触发应用回收任务
// @Tags         members
// @Produce      json
// @Security     BearerAuth
// @Param        userId  path      string  true  "成员用户 ID"
// @Success      204     "删除成功，无响应体"
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /members/{userId} [delete]
func (h *MembersHandler) Delete(c *gin.Context) {
	principal := principalFromCtx(c)
	if err := h.service.DeleteMember(c.Request.Context(), principal, c.Param("userId"), h.jobNotifier); err != nil {
		writeMemberError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func writeMemberError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrForbidden):
		c.JSON(http.StatusForbidden, apierror.New("FORBIDDEN", "无权执行该操作"))
	case errors.Is(err, service.ErrNotFound):
		c.JSON(http.StatusNotFound, apierror.New("NOT_FOUND", "资源不存在"))
	case errors.Is(err, service.ErrMemberCreateInvalid):
		c.JSON(http.StatusBadRequest, apierror.New("MEMBER_INVALID", validationServiceMessage(err, service.ErrMemberCreateInvalid)))
	case errors.Is(err, service.ErrNoNodeAvailable):
		// 自动选节点失败：当前没有 active 且剩余容量 > 0 的节点；前端需要展示明确文案让 ops 加节点或解禁。
		c.JSON(http.StatusServiceUnavailable, apierror.New("NO_NODE_AVAILABLE", "暂无可用 Runtime Node，请联系平台管理员调整节点容量或新增节点"))
	default:
		// 未知错误冒泡到日志，便于运维定位；响应仍保持脱敏。
		slog.ErrorContext(c.Request.Context(), "member handler 未识别错误", "error", err)
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "服务暂时不可用"))
	}
}
