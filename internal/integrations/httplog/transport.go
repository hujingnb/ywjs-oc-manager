// Package httplog 提供记录出站 HTTP 调用元数据的 RoundTripper，
// 供 newapi / ragflow 等 integration client 在传输层统一接入，
// 业务方法无需逐个手写调用日志。
package httplog

import (
	"log/slog"
	"net/http"
	"time"

	mlog "oc-manager/internal/log"
)

// transport 包装内层 RoundTripper，在每次出站请求后记录 log_type/method/endpoint/status/latency。
type transport struct {
	base    http.RoundTripper
	logType string // 依赖标识 / 日志类型，如 newapi / ragflow
}

// New 返回带日志的 RoundTripper；base 为 nil 时退回 http.DefaultTransport。
// logType 传 mlog.LogTypeNewAPI / mlog.LogTypeRAGFlow。
func New(base http.RoundTripper, logType string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &transport{base: base, logType: logType}
}

// RoundTrip 计时执行内层请求并记录元数据；不读取/不记录 body，错误原样透传。
func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	latency := time.Since(start).Milliseconds()

	// 用 req.Context() 作为 ctx，使外部调用日志自动带上发起方请求的 trace_id，实现链路串联。
	ctx := req.Context()
	attrs := []slog.Attr{
		slog.String(mlog.KeyLogType, t.logType),
		slog.String(mlog.KeyMethod, req.Method),
		slog.String(mlog.KeyEndpoint, req.URL.Path), // 仅 path，不含 query，避免敏感参数泄露
		slog.Int64(mlog.KeyLatencyMS, latency),
	}
	if err != nil {
		// 传输层错误（如连接失败）无状态码可记，只带 error 并升为 Warn，错误继续透传。
		attrs = append(attrs, mlog.Err(err))
		slog.LogAttrs(ctx, slog.LevelWarn, "external_request", attrs...)
		return resp, err
	}
	attrs = append(attrs, slog.Int(mlog.KeyStatus, resp.StatusCode))
	level := slog.LevelDebug
	if resp.StatusCode >= 300 {
		// 非 2xx（含 3xx 重定向与 4xx/5xx 错误）统一升为 Warn，便于排查外部依赖异常。
		level = slog.LevelWarn
	}
	slog.LogAttrs(ctx, level, "external_request", attrs...)
	return resp, err
}
