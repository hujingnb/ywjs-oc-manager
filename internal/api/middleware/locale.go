package middleware

import (
	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/i18n"
)

// Locale 解析请求 Accept-Language，归一到受支持 locale（回落 defaultLocale），
// 写入请求上下文供 apierror 写出错误时选择语言。
func Locale(defaultLocale string) gin.HandlerFunc {
	def := i18n.NormalizeLocale(defaultLocale, "en")
	return func(c *gin.Context) {
		apierror.SetLocale(c, i18n.ParseAcceptLanguage(c.GetHeader("Accept-Language"), def))
		c.Next()
	}
}
