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

func (h *requestIDHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := requestIDExtractor(ctx); id != "" {
		r.AddAttrs(slog.String("trace_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h *requestIDHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &requestIDHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *requestIDHandler) WithGroup(name string) slog.Handler {
	return &requestIDHandler{Handler: h.Handler.WithGroup(name)}
}

// NewSlogLogger 构造 manager-api / agent 顶层 logger。
//   - 输出：JSON handler，便于容器日志驱动 / ELK 解析
//   - 脱敏：Writer 经 NewRedactingWriter 包装（与现有 stdlib log 等价）
//   - source：AddSource=true 含 caller 路径，便于错误定位
//   - level：Info（生产足够；调试时未来可加 LOG_LEVEL env，本次不做）
//
// out 为 nil 时默认走 os.Stderr。
func NewSlogLogger(out io.Writer) *slog.Logger {
	if out == nil {
		out = os.Stderr
	}
	w := NewRedactingWriter(out)
	base := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:     slog.LevelInfo,
		AddSource: true,
	})
	return slog.New(&requestIDHandler{Handler: base})
}
