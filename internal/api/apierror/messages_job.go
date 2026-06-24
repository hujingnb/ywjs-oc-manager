package apierror

// 本文件集中登记「异步任务」（job）domain handler 层错误文案的 MsgKey 与中/英译文。
// 范围覆盖 internal/api/handlers/jobs.go 中内联的静态中文 apierror.New 调用：job id 非法、
// job 不存在、查询失败、无权查看、关联应用不存在 / 查询失败等。
// zh 译文逐字取自 handler 原中文（标点/空格不改），en 为忠实英译；相同中文复用同一 key
// （「无权查看 job」「job 关联应用不存在」各出现两处，复用同一 key）。
// 通用语义不在本文件重复定义，直接复用 messages_common.go 的对应 key。

// 异步任务 domain 静态错误 MsgKey 常量。
const (
	// MsgJobInvalidID 非法 job id。
	MsgJobInvalidID MsgKey = "err.job.invalid_id"
	// MsgJobNotFound job 不存在。
	MsgJobNotFound MsgKey = "err.job.not_found"
	// MsgJobQueryFailed 查询 job 失败。
	MsgJobQueryFailed MsgKey = "err.job.query_failed"
	// MsgJobForbidden 无权查看 job；payload 缺 app_id 与鉴权失败两处复用。
	MsgJobForbidden MsgKey = "err.job.forbidden"
	// MsgJobAppNotFound job 关联应用不存在；payload.app_id 非法与 app 查询无行两处复用。
	MsgJobAppNotFound MsgKey = "err.job.app_not_found"
	// MsgJobAppQueryFailed 查询 job 关联应用失败。
	MsgJobAppQueryFailed MsgKey = "err.job.app_query_failed"
)

// init 把异步任务 domain 错误译文并入中心 catalog。
func init() {
	Register(map[MsgKey]map[string]string{
		MsgJobInvalidID:      {"zh": "非法 job id", "en": "Invalid job id"},
		MsgJobNotFound:       {"zh": "job 不存在", "en": "The job does not exist"},
		MsgJobQueryFailed:    {"zh": "查询 job 失败", "en": "Failed to query the job"},
		MsgJobForbidden:      {"zh": "无权查看 job", "en": "You are not allowed to view this job"},
		MsgJobAppNotFound:    {"zh": "job 关联应用不存在", "en": "The application associated with the job does not exist"},
		MsgJobAppQueryFailed: {"zh": "查询 job 关联应用失败", "en": "Failed to query the application associated with the job"},
	})
}
