package log

import (
	"log/slog"
	"os"
	"strings"
)

// Config 控制顶层 logger 的输出行为，由 env 解析得到。
type Config struct {
	Level  slog.Level // 日志级别，低于此级别的记录被丢弃
	Format string     // 输出格式："json"（默认）或 "text"（本地调试友好）
}

// ParseConfigFromEnv 从 LOG_LEVEL / LOG_FORMAT 读取配置；非法值各自回退默认。
func ParseConfigFromEnv() Config {
	return Config{
		Level:  parseLevel(os.Getenv("LOG_LEVEL")),
		Format: parseFormat(os.Getenv("LOG_FORMAT")),
	}
}

// parseLevel 解析级别字符串，大小写不敏感并裁剪首尾空格；非法值回退 Info。
func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		// info 与一切非法值统一回退 Info，保证生产默认行为不变。
		return slog.LevelInfo
	}
}

// parseFormat 解析输出格式；非法值回退 json，保证容器日志可解析。
func parseFormat(s string) string {
	if strings.ToLower(strings.TrimSpace(s)) == "text" {
		return "text"
	}
	return "json"
}
