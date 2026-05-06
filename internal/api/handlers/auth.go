package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	redactlog "oc-manager/internal/log"
	"oc-manager/internal/service"
)

// AuthService 定义认证 handler 需要的业务能力。
// handler 只负责 HTTP DTO 和状态码映射，不直接访问数据库或签发 token。
type AuthService interface {
	Login(ctx context.Context, input service.LoginInput) (service.LoginResult, error)
	Refresh(ctx context.Context, refreshToken string) (service.LoginResult, error)
	Logout(ctx context.Context, refreshToken string) error
	Me(ctx context.Context, principal auth.Principal) (service.AuthUser, error)
}

// AuthHandler 承载认证相关 HTTP 路由。
type AuthHandler struct {
	service AuthService
	tokens  *auth.TokenManager
}

// NewAuthHandler 创建认证 handler。
func NewAuthHandler(service AuthService, tokens *auth.TokenManager) *AuthHandler {
	return &AuthHandler{service: service, tokens: tokens}
}

// RegisterAuthRoutes 注册认证路由。
func RegisterAuthRoutes(router gin.IRouter, handler *AuthHandler) {
	group := router.Group("/api/v1/auth")
	group.POST("/login", handler.Login)
	group.POST("/refresh", handler.Refresh)
	group.POST("/logout", handler.Logout)
	group.GET("/me", handler.Me)
}

type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// Login 处理用户名密码登录。
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数不完整"})
		return
	}
	result, err := h.service.Login(c.Request.Context(), service.LoginInput{
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil {
		writeAuthError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// Refresh 使用 refresh token 续期，并轮换 refresh token。
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数不完整"})
		return
	}
	result, err := h.service.Refresh(c.Request.Context(), req.RefreshToken)
	if err != nil {
		writeAuthError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// Logout 撤销 refresh token。
func (h *AuthHandler) Logout(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数不完整"})
		return
	}
	if err := h.service.Logout(c.Request.Context(), req.RefreshToken); err != nil {
		writeAuthError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Me 返回当前 access token 对应的用户信息。
func (h *AuthHandler) Me(c *gin.Context) {
	token, ok := bearerToken(c.GetHeader("Authorization"))
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "缺少访问令牌"})
		return
	}
	principal, err := h.tokens.VerifyAccessToken(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "访问令牌无效"})
		return
	}
	user, err := h.service.Me(c.Request.Context(), principal)
	if err != nil {
		writeAuthError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": user})
}

func bearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	return token, ok && strings.EqualFold(scheme, "Bearer") && token != ""
}

func writeAuthError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidCredentials):
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
	case errors.Is(err, service.ErrInvalidToken):
		c.JSON(http.StatusUnauthorized, gin.H{"error": "登录凭证无效"})
	case errors.Is(err, service.ErrUserDisabled), errors.Is(err, service.ErrOrgDisabled):
		c.JSON(http.StatusForbidden, gin.H{"error": redactlog.SafeErrorMessage(err)})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "认证服务暂时不可用"})
	}
}
