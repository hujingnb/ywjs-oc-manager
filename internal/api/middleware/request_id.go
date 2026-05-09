package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/gin-gonic/gin"
)

// RequestIDHeader 是请求与响应中携带 traceID 的 HTTP header 名。
const RequestIDHeader = "X-Request-ID"

// 不导出，避免外部 ctx 写入冲突；外部读取走 RequestIDFromContext 函数。
type ctxKey string

const requestIDKey ctxKey = "request_id"

// RequestID 中间件保证每个请求都有 trace_id：
//   - 优先沿用客户端 X-Request-ID header（便于跨服务串联）
//   - 否则生成 16 字节随机 hex（32 字符）
//   - 注入到 c.Request.Context()，下游 handler / service 可读
//   - 同时写入 response X-Request-ID header，让客户端能回报
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(RequestIDHeader)
		if id == "" {
			id = generateRequestID()
		}
		ctx := context.WithValue(c.Request.Context(), requestIDKey, id)
		c.Request = c.Request.WithContext(ctx)
		c.Header(RequestIDHeader, id)
		c.Next()
	}
}

// RequestIDFromContext 从 ctx 取 traceID；缺失返回空串。
// log/slog 层通过此函数（注入到 RequestIDExtractor）自动给日志附加 trace_id。
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

// generateRequestID 生成 16 字节随机 hex（32 字符）。
// crypto/rand.Read 在 Linux 上几乎不会失败；fallback 给固定标记便于排查。
func generateRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "norandom-fallback"
	}
	return hex.EncodeToString(b[:])
}
