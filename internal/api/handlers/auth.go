package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
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
	ChangePassword(ctx context.Context, principal auth.Principal, input service.ChangePasswordInput) error
	// UpdateLocale 持久化当前用户的界面语言偏好；locale 须属于受支持集合，否则返回 service.ErrInvalidLocale。
	UpdateLocale(ctx context.Context, userID, locale string) error
}

// AuthHandler 承载认证相关 HTTP 路由。
type AuthHandler struct {
	service AuthService
	// captcha 为出题器；nil 表示验证码关闭，出题接口返回 204。
	// 用具体类型而非接口，规避 Go typed-nil 接口陷阱（nil 指针装箱成非 nil 接口）。
	captcha *service.CaptchaService
}

// NewAuthHandler 创建认证 handler。captcha 为 nil 时出题接口返回 204、登录不校验验证码。
func NewAuthHandler(svc AuthService, captcha *service.CaptchaService) *AuthHandler {
	return &AuthHandler{service: svc, captcha: captcha}
}

// RegisterPublicAuthRoutes 注册无需 Bearer token 的认证路由（public 分组）。
// login/refresh/logout 均使用请求体携带凭证，不依赖 access token。
// altcha-challenge 用于登录前向前端下发 PoW 挑战，同样不需要鉴权。
func RegisterPublicAuthRoutes(router gin.IRouter, handler *AuthHandler) {
	group := router.Group("/api/v1/auth")
	group.POST("/login", handler.Login)
	group.POST("/refresh", handler.Refresh)
	group.POST("/logout", handler.Logout)
	group.GET("/altcha-challenge", handler.AltchaChallenge)
}

// RegisterAuthMeRoutes 注册需要认证的 auth 路由（user 分组，已受 RequireUserAuth 保护）。
func RegisterAuthMeRoutes(router gin.IRouter, handler *AuthHandler) {
	group := router.Group("/api/v1/auth")
	group.GET("/me", handler.Me)
	group.POST("/password", handler.ChangePassword)
	group.PATCH("/me/locale", handler.UpdateLocale)
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
		Captcha:  req.Captcha,
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
	// principal 由 RequireUserAuth 中间件注入；此处只做账号状态二次查库。
	principal := principalFromCtx(c)
	user, err := h.service.Me(c.Request.Context(), principal)
	if err != nil {
		writeAuthError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": user})
}

// ChangePassword 修改当前登录用户自己的密码。
//
// @Summary      修改当前用户密码
// @Description  已登录用户输入当前密码后修改自己的 manager 登录密码
// @Tags         auth
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body  ChangePasswordRequest  true  "修改密码请求"
// @Success      204   "密码修改成功，无响应体"
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      403   {object}  ErrorResponse
// @Failure      500   {object}  ErrorResponse
// @Router       /auth/password [post]
func (h *AuthHandler) ChangePassword(c *gin.Context) {
	// principal 由认证中间件注入，service 会再次校验用户和组织状态。
	principal := principalFromCtx(c)
	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	if err := h.service.ChangePassword(c.Request.Context(), principal, service.ChangePasswordInput{
		OldPassword: req.OldPassword,
		NewPassword: req.NewPassword,
	}); err != nil {
		writeAuthError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// UpdateLocale 持久化当前登录用户的界面语言偏好。
//
// @Summary      更新界面语言
// @Description  保存当前用户的界面语言偏好（en/zh），登录后跟随用户
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      UpdateLocaleRequest  true  "语言请求"
// @Success      204
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Router       /auth/me/locale [patch]
func (h *AuthHandler) UpdateLocale(c *gin.Context) {
	var req UpdateLocaleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	principal := principalFromCtx(c)
	if err := h.service.UpdateLocale(c.Request.Context(), principal.UserID, req.Locale); err != nil {
		if errors.Is(err, service.ErrInvalidLocale) {
			apierror.JSON(c, http.StatusBadRequest, "INVALID_LOCALE", apierror.MsgAuthInvalidLocale)
			c.Abort()
			return
		}
		writeAuthError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// writeAuthError 将认证 service 的 sentinel error 映射为 HTTP 状态码。
// 禁用用户和禁用组织返回 403，避免前端误判为 token 过期并循环刷新。
func writeAuthError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidCredentials):
		apierror.JSON(c, http.StatusUnauthorized, "INVALID_CREDENTIALS", apierror.MsgAuthInvalidCredentials)
	case errors.Is(err, service.ErrInvalidToken):
		apierror.JSON(c, http.StatusUnauthorized, "INVALID_TOKEN", apierror.MsgAuthInvalidToken)
	case errors.Is(err, service.ErrUserDisabled), errors.Is(err, service.ErrOrgDisabled):
		code := "USER_DISABLED"
		if errors.Is(err, service.ErrOrgDisabled) {
			code = "ORG_DISABLED"
		}
		// 禁用用户/组织的具体原因由 service 脱敏后透出，保留运行期动态文案，不进 catalog。
		c.JSON(http.StatusForbidden, apierror.New(code, redactlog.SafeErrorMessage(err)))
	case errors.Is(err, service.ErrMemberCreateInvalid):
		// 成员创建校验失败的字段级原因运行期生成，保留动态文案，不进 catalog。
		c.JSON(http.StatusBadRequest, apierror.New("MEMBER_INVALID", validationServiceMessage(err, service.ErrMemberCreateInvalid)))
	case errors.Is(err, service.ErrCaptchaRequired):
		apierror.JSON(c, http.StatusBadRequest, "CAPTCHA_REQUIRED", apierror.MsgAuthCaptchaRequired)
	case errors.Is(err, service.ErrCaptchaInvalid):
		apierror.JSON(c, http.StatusBadRequest, "CAPTCHA_INVALID", apierror.MsgAuthCaptchaExpired)
	case errors.Is(err, service.ErrCaptchaReplayed):
		apierror.JSON(c, http.StatusBadRequest, "CAPTCHA_REPLAYED", apierror.MsgAuthCaptchaExpired)
	default:
		apierror.JSON(c, http.StatusInternalServerError, "INTERNAL", apierror.MsgAuthServiceUnavailable)
	}
}

// AltchaChallenge 下发一道 Altcha 挑战；验证码关闭时返回 204。
//
// @Summary      Altcha 挑战
// @Description  返回登录页验证码挑战；验证码未启用时返回 204
// @Tags         auth
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "Altcha 挑战 JSON"
// @Success      204  "验证码未启用"
// @Failure      500  {object}  ErrorResponse
// @Router       /auth/altcha-challenge [get]
func (h *AuthHandler) AltchaChallenge(c *gin.Context) {
	// 挑战与关闭态都不允许缓存：挑战只能消费一次，204 缓存会让前端误以为验证码仍关闭。
	c.Header("Cache-Control", "no-store, max-age=0")
	if h.captcha == nil {
		c.Status(http.StatusNoContent)
		return
	}
	challenge, err := h.captcha.Challenge()
	if err != nil {
		apierror.JSON(c, http.StatusInternalServerError, "CAPTCHA_CHALLENGE_FAILED", apierror.MsgAuthCaptchaChallengeFailed)
		return
	}
	c.JSON(http.StatusOK, challenge)
}
