package log

import (
	"context"
	"io"
	"log/slog"
	"os"
)

// RequestIDExtractor 从 ctx 中抽 trace_id 字符串；缺失返回空串。
// 由 internal/api/middleware 在程序启动期通过 SetRequestIDExtractor 注入实际实现，
// 用函数指针解耦避免 internal/log 直接 import middleware 形成循环依赖。
type RequestIDExtractor func(context.Context) string

// 默认实现：空串（即不附加 trace_id）。启动期由 main.go 调用 SetRequestIDExtractor 替换。
var requestIDExtractor RequestIDExtractor = func(context.Context) string { return "" }

// SetRequestIDExtractor 注入 trace_id 提取函数。仅在 main.go 启动期调用一次。
func SetRequestIDExtractor(fn RequestIDExtractor) {
	if fn != nil {
		requestIDExtractor = fn
	}
}

// requestIDHandler 包装 slog.Handler，自动从 ctx 提取 trace_id 并附加到 record 中。
type requestIDHandler struct {
	slog.Handler
}

// Handle 在写日志前追加 trace_id，缺失时保持原始 record 不变。
func (h *requestIDHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := requestIDExtractor(ctx); id != "" {
		r.AddAttrs(slog.String("trace_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

// WithAttrs 保持 requestIDHandler 包装，避免 slog 派生 handler 后丢失 trace_id 注入。
func (h *requestIDHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &requestIDHandler{Handler: h.Handler.WithAttrs(attrs)}
}

// WithGroup 保持 requestIDHandler 包装，确保分组日志仍能自动带 trace_id。
func (h *requestIDHandler) WithGroup(name string) slog.Handler {
	return &requestIDHandler{Handler: h.Handler.WithGroup(name)}
}

// NewSlogLogger 构造 manager-api / agent 顶层 logger。
//   - 输出：cfg.Format 为 "text" 时用 TextHandler（本地调试），否则 JSONHandler（容器日志/ELK）
//   - 级别：cfg.Level（由 LOG_LEVEL 解析，默认 Info）
//   - 脱敏：Writer 经 NewRedactingWriter 包装，json/text 两种格式都生效
//   - trace_id：requestIDHandler 自动从 ctx 注入，不受格式影响
//   - source：AddSource=true 含 caller 路径，便于错误定位
//
// out 为 nil 时默认走 os.Stderr。
func NewSlogLogger(out io.Writer, cfg Config) *slog.Logger {
	if out == nil {
		out = os.Stderr
	}
	w := NewRedactingWriter(out)
	opts := &slog.HandlerOptions{
		Level:     cfg.Level,
		AddSource: true,
	}
	var base slog.Handler
	if cfg.Format == "text" {
		base = slog.NewTextHandler(w, opts)
	} else {
		base = slog.NewJSONHandler(w, opts)
	}
	return slog.New(&requestIDHandler{Handler: base})
}
