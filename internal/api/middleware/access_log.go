package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	mlog "oc-manager/internal/log"
)

// skipAccessLogPaths 是纯噪音、不记 access log 的路径（健康检查 / 就绪探针）。
// 与 handlers.RegisterHealthRoutes 注册的路径保持一致：当前只有 /healthz，
// /readyz 作为未来就绪探针的预留项，提前在此屏蔽以免后续遗漏。
var skipAccessLogPaths = map[string]bool{
	"/healthz": true,
	"/readyz":  true,
}

// AccessLog 在每个 HTTP 请求结束后记录一条结构化访问日志（仅 stdout）。
//
// 字段：method / route（路由模板，避免真实 ID 进日志致基数爆炸）/ status /
// latency_ms / client_ip / user_id（鉴权后从 ctx 取，未鉴权为空）/ bytes。
// trace_id 由 internal/log 的 requestIDHandler 自动注入。
//
// 级别：5xx→Error，4xx→Warn，其余→Info。健康检查路径跳过。
//
// 必须挂在 RequireUserAuth 之后，才能在 c.Next() 返回后读到 principal。
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		if skipAccessLogPaths[c.Request.URL.Path] {
			c.Next()
			return
		}
		start := time.Now()
		c.Next()

		status := c.Writer.Status()
		// route 用 gin 路由模板；未匹配路由（404）FullPath 为空，回退原始 path。
		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}
		var userID string
		if p, ok := auth.PrincipalFromContext(c.Request.Context()); ok {
			userID = p.UserID
		}

		level := slog.LevelInfo
		switch {
		case status >= 500:
			level = slog.LevelError
		case status >= 400:
			level = slog.LevelWarn
		}

		slog.LogAttrs(c.Request.Context(), level, "http_request",
			slog.String(mlog.KeyMethod, c.Request.Method),
			slog.String(mlog.KeyRoute, route),
			slog.Int(mlog.KeyStatus, status),
			slog.Int64(mlog.KeyLatencyMS, time.Since(start).Milliseconds()),
			slog.String(mlog.KeyClientIP, c.ClientIP()),
			slog.String(mlog.KeyUserID, userID),
			slog.Int(mlog.KeyBytes, c.Writer.Size()),
		)
	}
}
