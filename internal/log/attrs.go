package log

import "log/slog"

// 统一 attr key 常量，避免各处字符串字面量漂移；新增日志字段优先复用这里。
const (
	KeyService    = "service"     // 外部依赖标识：newapi / ragflow
	KeyMethod     = "method"      // HTTP 方法
	KeyRoute      = "route"       // gin 路由模板（非真实路径，避免 ID 进日志）
	KeyEndpoint   = "endpoint"    // 外部调用路径（不含 query）
	KeyStatus     = "status"      // HTTP 状态码
	KeyLatencyMS  = "latency_ms"  // 处理耗时（毫秒）
	KeyClientIP   = "client_ip"   // 客户端 IP
	KeyUserID     = "user_id"     // 请求主体用户 ID
	KeyOrgID      = "org_id"      // 组织 ID
	KeyActorID    = "actor_id"    // 操作者 ID（审计语义）
	KeyTargetType = "target_type" // 操作目标类型
	KeyTargetID   = "target_id"   // 操作目标 ID
	KeyAction     = "action"      // 业务动作
	KeyBytes      = "bytes"       // 响应字节数
	KeyError      = "error"       // 错误信息统一 key
)

// Err 把 error 包装成统一 key 的 slog.Attr；nil 时值为空串，避免调用方判空。
func Err(err error) slog.Attr {
	if err == nil {
		return slog.String(KeyError, "")
	}
	return slog.String(KeyError, err.Error())
}
