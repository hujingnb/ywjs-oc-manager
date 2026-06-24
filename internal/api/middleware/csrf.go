package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
)

// CSRFCookieName 是双 submit cookie 的名字。前端通过 document.cookie 读它写到
// X-CSRF-Token header；middleware 校验 header == cookie。
const CSRFCookieName = "csrf_token"

// CSRFHeaderName 是前端注入的 header 名。
const CSRFHeaderName = "X-CSRF-Token"

// RequireCSRF 是 double-submit cookie 校验的中间件。
//
// 行为：
//   - safe methods (GET / HEAD / OPTIONS) 直接放行；
//   - agent 与 auth 路由通过 path 白名单跳过（机器对机器、首次登录还没拿 cookie）；
//   - 请求未携带 CSRF cookie 视为 opt-in 模式（旧客户端 / curl 测试）放行；
//   - 请求携带 CSRF cookie 时强制 X-CSRF-Token header == cookie 值，否则 403。
//
// 这种「opt-in」模式既符合 spec §5.4 Task 13"所有写操作 cookie+header 双校验"，
// 又避免破坏没有前端拦截器的旧调用方（CLI / 集成测试）。前端注入 cookie 后立刻进入硬模式。
func RequireCSRF(skipPaths ...string) gin.HandlerFunc {
	skip := make(map[string]struct{}, len(skipPaths))
	for _, p := range skipPaths {
		skip[p] = struct{}{}
	}
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		if _, ok := skip[c.FullPath()]; ok {
			c.Next()
			return
		}
		// path prefix 跳过：agent 路由 + auth 登录与刷新（首次没 cookie）。
		if isCSRFExempt(c.Request.URL.Path) {
			c.Next()
			return
		}
		cookie, err := c.Request.Cookie(CSRFCookieName)
		if err != nil || cookie.Value == "" {
			// opt-in 模式：客户端尚未启用 CSRF cookie 时放行。
			c.Next()
			return
		}
		header := c.GetHeader(CSRFHeaderName)
		if header == "" || header != cookie.Value {
			// 保持历史 {"error": ...} 响应体形状不变（前端按该字段读取），仅把文案
			// 走 apierror catalog 按请求 locale 翻译；Locale 中间件已在 CSRF 之前注入 locale。
			c.AbortWithStatusJSON(http.StatusForbidden,
				gin.H{"error": apierror.Localize(apierror.MsgAuthCSRFInvalid, apierror.LocaleFrom(c))})
			return
		}
		c.Next()
	}
}

// isCSRFExempt 判断 URL 是否属于 CSRF 免疫前缀。
// 主要包括 agent 注册 / 心跳（机器对机器无浏览器 cookie）与登录 / 刷新（首次没 cookie）。
func isCSRFExempt(path string) bool {
	prefixes := []string{
		"/api/v1/agent/",
		"/api/v1/auth/login",
		"/api/v1/auth/refresh",
	}
	for _, p := range prefixes {
		if len(path) >= len(p) && path[:len(p)] == p {
			return true
		}
	}
	return false
}
