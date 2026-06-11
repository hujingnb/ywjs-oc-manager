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
	KeyLogType    = "log_type"    // 日志类型分类：http / sql / newapi / ragflow / app
	KeySQL        = "sql"         // SQL 语句文本（已参数化，不含真实参数值）
	KeyRows       = "rows"        // SQL 写操作影响行数（仅 ExecContext 记录）
)

// log_type 取值常量。基础设施类（http/sql/newapi/ragflow）在各自调用点显式带，
// 业务及其它普通日志统一为 app，由 requestIDHandler 兜底注入，避免逐条手填。
const (
	LogTypeHTTP    = "http"    // access log 中间件记录的 HTTP 请求
	LogTypeSQL     = "sql"     // SQL 日志
	LogTypeNewAPI  = "newapi"  // 调用 new-api 的出站请求
	LogTypeRAGFlow = "ragflow" // 调用 RAGFlow 的出站请求
	LogTypeApp     = "app"     // 业务及其它普通日志（兜底类型）
)

// Err 把 error 包装成统一 key 的 slog.Attr；nil 时值为空串，避免调用方判空。
func Err(err error) slog.Attr {
	if err == nil {
		return slog.String(KeyError, "")
	}
	return slog.String(KeyError, err.Error())
}
