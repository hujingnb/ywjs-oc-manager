package handlers

import (
	"context"
	"errors"
	"log/slog"
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
// 错误映射：缺/无效 token → 401；path id 不一致 → 401；app 未就绪 → 409；
// 普通应用缺对象存储 → 503；不支持 app_type → 422；其他组装失败 → 500。
func (h *BootstrapHandler) Bootstrap(c *gin.Context) {
	// 取 Bearer token；bearerToken 辅助函数定义于本文件。
	token, ok := bearerToken(c.GetHeader("Authorization"))
	if !ok {
		apierror.JSON(c, http.StatusUnauthorized, "UNAUTHORIZED", apierror.MsgBootstrapMissingToken)
		return
	}

	// 按 token hash 反查 app；token 无效或查无此 app 一律 401，不泄露 app 是否存在。
	app, err := h.service.ResolveByControlToken(c.Request.Context(), service.HashAppRuntimeToken(token))
	if err != nil {
		apierror.JSON(c, http.StatusUnauthorized, "UNAUTHORIZED", apierror.MsgBootstrapInvalidToken)
		return
	}

	// 校验 path id 与 token 归属 app 一致，防止持 A 的 token 拉取 B 的配置（横向越权）。
	if app.ID != c.Param("id") {
		apierror.JSON(c, http.StatusUnauthorized, "UNAUTHORIZED", apierror.MsgBootstrapTokenMismatch)
		return
	}

	// 组装 bootstrap 响应：manifest YAML + 预签名 URL + STS 临时写凭证。
	res, err := h.service.Build(c.Request.Context(), app)
	if err != nil {
		if errors.Is(err, service.ErrAppNotReady) {
			// app 缺少 api_key / control token 或尚无发布版本，pod 应稍后重试。
			apierror.JSON(c, http.StatusConflict, "APP_NOT_READY", apierror.MsgBootstrapAppNotReady)
			return
		}
		if errors.Is(err, service.ErrStandardAppBootstrapRequiresObjectStorage) {
			// 普通应用缺少 S3 / skill 依赖时暂不可启动，调用方可在依赖恢复后重试。
			apierror.JSON(c, http.StatusServiceUnavailable, "BOOTSTRAP_OBJECT_STORAGE_REQUIRED", apierror.MsgBootstrapObjectStorageRequired)
			return
		}
		if errors.Is(err, service.ErrUnsupportedBootstrapAppType) {
			// 未知应用类型无法安全推断数据下发权限，调用方必须先修正应用数据。
			apierror.JSON(c, http.StatusUnprocessableEntity, "UNSUPPORTED_APP_TYPE", apierror.MsgBootstrapUnsupportedAppType)
			return
		}
		// 记录具体内部错误便于运维定位（如 S3 endpoint 缺 scheme、依赖不可达）：对外仍只回
		// 泛化 message 不泄露细节，但日志带上 app id 与底层 err，免去再复现 bootstrap 的麻烦。
		slog.ErrorContext(c.Request.Context(), "bootstrap 组装失败", "app_id", app.ID, "error", err)
		apierror.JSON(c, http.StatusInternalServerError, "INTERNAL", apierror.MsgBootstrapAssembleFailed)
		return
	}
	c.JSON(http.StatusOK, res)
}
