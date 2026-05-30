package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// bearerToken 从 Authorization header 中提取 Bearer token。
// header 格式必须为 "Bearer <token>"，scheme 大小写不敏感；token 为空时返回 false。
func bearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	return token, ok && strings.EqualFold(scheme, "Bearer") && token != ""
}

// BootstrapAppService 是 bootstrap handler 所需的服务能力（窄接口，便于测试注入）。
type BootstrapAppService interface {
	// ResolveByControlToken 用 control token hash 反查 app；用于鉴权即定位。
	// Task 13 将在 BootstrapService 上实现此方法；handler 接口在此声明，编译期解耦。
	ResolveByControlToken(ctx context.Context, tokenHash string) (sqlc.App, error)
	// Build 组装 bootstrap 响应（manifest + 预签名 URL + STS 凭证）。
	Build(ctx context.Context, app sqlc.App) (service.BootstrapResult, error)
}

// BootstrapHandler 处理 pod 启动回调 GET /internal/apps/{id}/bootstrap。
type BootstrapHandler struct {
	service BootstrapAppService
}

// NewBootstrapHandler 构造 bootstrap handler。
func NewBootstrapHandler(svc BootstrapAppService) *BootstrapHandler {
	return &BootstrapHandler{service: svc}
}

// RegisterBootstrapRoutes 注册内部路由 /internal/apps/:id/bootstrap。
// 该组不挂用户鉴权中间件，由 handler 内联校验 control token，不进 openapi。
func RegisterBootstrapRoutes(router gin.IRouter, handler *BootstrapHandler) {
	group := router.Group("/internal")
	group.GET("/apps/:id/bootstrap", handler.Bootstrap)
}

// Bootstrap 校验 control token 并返回组装后的 bootstrap 响应。
//
// 鉴权流程：
//  1. 从 Authorization header 取 Bearer token（调用包内 bearerToken 辅助）。
//  2. 对 token 做 hash，调用 service.ResolveByControlToken 反查 app（hash 不匹配即报 401）。
//  3. 校验 path :id 与 token 所属 app.ID 一致，防止持 A 的 token 拉 B 的配置。
//
// 错误映射：缺/无效 token → 401；path id 不一致 → 401；app 未就绪 → 409；其他 → 500。
func (h *BootstrapHandler) Bootstrap(c *gin.Context) {
	// 取 Bearer token；bearerToken 辅助函数定义于本文件。
	token, ok := bearerToken(c.GetHeader("Authorization"))
	if !ok {
		c.JSON(http.StatusUnauthorized, apierror.New("UNAUTHORIZED", "缺少 control token"))
		return
	}

	// 按 token hash 反查 app；token 无效或查无此 app 一律 401，不泄露 app 是否存在。
	app, err := h.service.ResolveByControlToken(c.Request.Context(), service.HashAppRuntimeToken(token))
	if err != nil {
		c.JSON(http.StatusUnauthorized, apierror.New("UNAUTHORIZED", "control token 无效"))
		return
	}

	// 校验 path id 与 token 归属 app 一致，防止持 A 的 token 拉取 B 的配置（横向越权）。
	if app.ID != c.Param("id") {
		c.JSON(http.StatusUnauthorized, apierror.New("UNAUTHORIZED", "control token 与目标 app 不匹配"))
		return
	}

	// 组装 bootstrap 响应：manifest YAML + 预签名 URL + STS 临时写凭证。
	res, err := h.service.Build(c.Request.Context(), app)
	if err != nil {
		if errors.Is(err, service.ErrAppNotReady) {
			// app 缺少 api_key / control token 或尚无发布版本，pod 应稍后重试。
			c.JSON(http.StatusConflict, apierror.New("APP_NOT_READY", "app 未就绪"))
			return
		}
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "bootstrap 组装失败"))
		return
	}
	c.JSON(http.StatusOK, res)
}
