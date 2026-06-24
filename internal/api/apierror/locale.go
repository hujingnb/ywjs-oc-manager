package apierror

import "github.com/gin-gonic/gin"

// localeContextKey 是 locale 在 gin.Context 中的存取键；由 locale 中间件写入，
// apierror 写出错误时读取。契约放在 apierror 包，避免 middleware ↔ apierror 反向依赖。
const localeContextKey = "oc_locale"

// SetLocale 由 locale 中间件调用，把归一后的 locale 写入请求上下文。
func SetLocale(c *gin.Context, loc string) { c.Set(localeContextKey, loc) }

// LocaleFrom 读取请求 locale；缺失时回落 "en"（保证任何路径都有确定语言）。
func LocaleFrom(c *gin.Context) string {
	if c == nil {
		return "en"
	}
	if v, ok := c.Get(localeContextKey); ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return "en"
}
