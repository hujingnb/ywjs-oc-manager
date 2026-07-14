// errors.go — ocops 包的哨兵错误定义与 HTTP 状态码映射。
//
// oc-ops 服务端按契约返回固定状态码（400/401/404/409/500/502），
// manager 侧 Client.DoJSON 统一经 statusToErr 转为哨兵错误，
// 上层 service 再将其映射为自身的业务错误。
package ocops

import "errors"

// HTTPStatusError 保留非 2xx 响应的真实 HTTP 状态，同时允许上层继续 errors.Is 匹配既有哨兵错误。
// 调度型调用依赖该状态区分限流/过载与确定性失败，不能只留下 ErrCLI。
type HTTPStatusError struct {
	StatusCode int
	Err        error
}

func (e *HTTPStatusError) Error() string { return e.Err.Error() }
func (e *HTTPStatusError) Unwrap() error { return e.Err }

// 哨兵错误：与 oc-ops HTTP 状态码 / 契约错误码一一对应，供 service 映射成自身哨兵错误。
var (
	// ErrBadRequest 对应 HTTP 400，请求参数非法。
	ErrBadRequest = errors.New("ocops: bad request") // 400
	// ErrNotFound 对应 HTTP 404，资源不存在。
	ErrNotFound = errors.New("ocops: not found") // 404
	// ErrUnsupported 对应 HTTP 409，操作在当前镜像/状态下不支持。
	ErrUnsupported = errors.New("ocops: unsupported") // 409
	// ErrOutputInvalid 对应 HTTP 500，服务内部错误或响应格式无效。
	ErrOutputInvalid = errors.New("ocops: internal/invalid") // 500
	// ErrCLI 对应 HTTP 502 及未知非 2xx 状态，hermes CLI 调用失败。
	ErrCLI = errors.New("ocops: hermes cli failed") // 502 及未知
	// ErrUnauthorized 对应 HTTP 401，Bearer token 校验失败。
	ErrUnauthorized = errors.New("ocops: unauthorized") // 401
)

// statusToErr 把 HTTP 状态码映射成哨兵错误；2xx 返回 nil。
// 未列举的非 2xx 状态兜底为 ErrCLI（与 Python 侧 default→502 语义一致）。
func statusToErr(status int) error {
	switch status {
	case 400:
		return ErrBadRequest
	case 401:
		return ErrUnauthorized
	case 404:
		return ErrNotFound
	case 409:
		return ErrUnsupported
	case 500:
		return ErrOutputInvalid
	default:
		// 2xx 成功，无错误
		if status >= 200 && status < 300 {
			return nil
		}
		// 其余非 2xx（含 502 及其它上游/网关错误）统一映射为 CLI 失败
		return ErrCLI
	}
}
