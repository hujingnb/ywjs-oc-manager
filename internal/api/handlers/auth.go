package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/middleware"
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

// Login 处理用户名密码登录。
//
// @Summary      登录
// @Description  返回 access_token + refresh_token + 用户信息快照
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      LoginRequest           true  "登录请求"
// @Success      200   {object}  service.LoginResult
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Router       /auth/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	// 登录接口没有 Bearer token，认证错误统一由 service 映射为 401。
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	result, err := h.service.Login(c.Request.Context(), service.LoginInput{
		OrgCode:  req.OrgCode,
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil {
		writeAuthError(c, err)
		return
	}
	setCSRFCookie(c, result.Tokens.AccessToken)
	c.JSON(http.StatusOK, result)
}

// Refresh 使用 refresh token 续期，并轮换 refresh token。
//
// @Summary      刷新令牌
// @Description  使用 refresh_token 换取新的 access_token + refresh_token
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      RefreshRequest         true  "刷新请求"
// @Success      200   {object}  service.LoginResult
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Router       /auth/refresh [post]
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	result, err := h.service.Refresh(c.Request.Context(), req.RefreshToken)
	if err != nil {
		writeAuthError(c, err)
		return
	}
	setCSRFCookie(c, result.Tokens.AccessToken)
	c.JSON(http.StatusOK, result)
}

// setCSRFCookie 为浏览器 set 一个非 HttpOnly 的 csrf_token cookie，
// 前端 axios 拦截器读它写到 X-CSRF-Token header 完成 double-submit 校验。
// 值复用 access_token 末 32 位字符（已经是高熵），避免再多生成一个独立随机源。
func setCSRFCookie(c *gin.Context, accessToken string) {
	if accessToken == "" {
		return
	}
	value := accessToken
	if len(value) > 32 {
		value = value[len(value)-32:]
	}
	// HttpOnly=false 让前端 JS 读得到；Secure=false 让本地 http://localhost 调试能用，
	// 生产环境部署在 https 反代后建议改 Secure=true。
	c.SetCookie(middleware.CSRFCookieName, value, 8*60*60, "/", "", false, false)
}

// Logout 撤销 refresh token。
//
// @Summary      登出
// @Description  撤销 refresh_token，使其不可再续期
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  RefreshRequest  true  "包含 refresh_token 的请求体"
// @Success      204
// @Failure      400  {object}  ErrorResponse
// @Failure      401  {object}  ErrorResponse
// @Router       /auth/logout [post]
func (h *AuthHandler) Logout(c *gin.Context) {
	var req RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	if err := h.service.Logout(c.Request.Context(), req.RefreshToken); err != nil {
		writeAuthError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Me 返回当前 access token 对应的用户信息。
//
// @Summary      当前用户信息
// @Description  返回当前 access token 对应的用户档案
// @Tags         auth
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]service.AuthUser
// @Failure      401  {object}  ErrorResponse
// @Router       /auth/me [get]
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
	// token 只证明调用者身份，账号是否仍可用由 AuthService.Me 再查库判断。
	user, err := h.service.Me(c.Request.Context(), principal)
	if err != nil {
		writeAuthError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": user})
}

// bearerToken 从 Authorization header 中提取 Bearer token。
// scheme 比较大小写不敏感；缺失或空 token 统一返回 false。
func bearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	return token, ok && strings.EqualFold(scheme, "Bearer") && token != ""
}

// writeAuthError 将认证 service 的 sentinel error 映射为 HTTP 状态码。
// 禁用用户和禁用组织返回 403，避免前端误判为 token 过期并循环刷新。
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
