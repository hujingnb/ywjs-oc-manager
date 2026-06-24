package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
)

// RequireUserAuth 校验 Authorization: Bearer <access_token>，把 Principal
// 写入 c.Request.Context() 并交给下游 handler；校验失败时直接 c.AbortWithStatusJSON
// 返回 401，下游 handler 不会执行。
//
// 设计取舍：
//   - 不做角色 / 资源权限判断，仅校验"凭证有效性"。资源 / 角色级权限仍由
//     service 层借助 authorizer.Can* 完成，避免 middleware 提前 403 误伤
//     跨组织的合法数据访问。
//   - 多种失败原因（缺失 header / 非 Bearer scheme / 空 token / 签名错 /
//     过期）统一返回 401 + code=UNAUTHENTICATED，不暴露具体原因，避免向
//     探测者泄露 token 细节。
//   - 中间件挂载顺序：RequestID → CSRF → RequireUserAuth；RequestID 必须
//     在前以便后续日志携带 trace_id；CSRF 必须在 auth 前完成双 submit cookie
//     校验，避免 token 校验通过但 CSRF 漏拦的写请求。
//   - 仅 user 路由组使用本中间件。public 组（健康检查、登录、刷新）与
//     internal 组（pod bootstrap 回调，由 handler 内联校验 control token）不挂载。
func RequireUserAuth(tokens *auth.TokenManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := parseBearer(c.GetHeader("Authorization"))
		if !ok {
			abortUnauthenticated(c, apierror.MsgAuthMissingToken)
			return
		}
		principal, err := tokens.VerifyAccessToken(token)
		if err != nil {
			abortUnauthenticated(c, apierror.MsgAuthAccessTokenInvalid)
			return
		}
		ctx := auth.WithPrincipal(c.Request.Context(), principal)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// parseBearer 解析 Authorization header 中的 Bearer token。
// 支持 scheme 大小写不敏感（"Bearer" / "bearer" / "BEARER" 均合法），
// 但 token 部分必须非空，否则视为缺失。
func parseBearer(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	return token, ok && strings.EqualFold(scheme, "Bearer") && token != ""
}

// abortUnauthenticated 用统一的 401 ErrorResponse 终止请求；
// 所有未认证失败都用同一个 code，避免暴露失败细节给探测者。
// key 走 apierror catalog 按请求 locale 翻译文案（Locale 中间件已在 auth 之前
// 注入 locale，故此处能拿到正确语言；缺失回落 en）。
func abortUnauthenticated(c *gin.Context, key apierror.MsgKey) {
	c.AbortWithStatusJSON(http.StatusUnauthorized,
		apierror.New("UNAUTHENTICATED", apierror.Localize(key, apierror.LocaleFrom(c))))
}
