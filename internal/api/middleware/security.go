// Package middleware 提供 manager API 的横切安全策略。
//
// 设计：
//   - CORS 白名单仅允许前端 public_base_url；缺失时不开启 CORS（同源部署）；
//   - 日志脱敏：把 query/header/body 中的 Authorization、agent_token、newapi key
//     等敏感字段替换为 ***，避免误入审计或 stdout；
//   - rate-limit / CSRF token 暂保留接口形态，由后续 task 按需启用。
package middleware

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORSAllowOrigin 返回一个简单的 CORS 中间件：
// 仅当请求 Origin == 配置允许列表时回写允许头；其它请求保持 same-origin 默认。
func CORSAllowOrigin(allowed []string) gin.HandlerFunc {
	allowMap := make(map[string]struct{}, len(allowed))
	for _, origin := range allowed {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}
		allowMap[origin] = struct{}{}
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if _, ok := allowMap[origin]; ok {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Authorization,Content-Type")
			if c.Request.Method == http.MethodOptions {
				c.AbortWithStatus(http.StatusNoContent)
				return
			}
		}
		c.Next()
	}
}

// SecretPatterns 是日志输出前需要脱敏的字段名匹配。
// 任何含有这些子串的 query / header / body 片段都会被替换为 ***。
var SecretPatterns = []string{
	"authorization", "agent_token", "bootstrap_token",
	"newapi_admin_token", "csrf_secret", "jwt_access_secret", "jwt_refresh_secret",
	"master_key", "password", "newapi_key_ciphertext",
}

// secretRegex 匹配 "key=value" 或 "key: value" 形式中的敏感字段，
// 替换 value 部分为 ***。
var secretRegex = regexp.MustCompile(`(?i)((` +
	strings.Join(SecretPatterns, "|") +
	`)["']?\s*[:=]\s*"?)[^",;}\n]+`)

// MaskSecret 把字符串中匹配 SecretPatterns 的字段值替换为 ***。
// 适合在日志输出前调用；不会修改原始字符串。
func MaskSecret(input string) string {
	return secretRegex.ReplaceAllString(input, "$1***")
}
