package apierror

// 本文件集中登记「web-publish 站点管理」handler 层错误文案的 MsgKey 与中/英译文。
// 范围覆盖 internal/api/handlers/web_publish_sites.go 中的静态错误映射。

// web-publish 站点管理静态错误 MsgKey 常量（前缀 err.web_publish.*）。
const (
	// MsgWebPublishNotProvisioned 企业 web-publish 能力未开通或尚未就绪（HTTP 403）。
	MsgWebPublishNotProvisioned MsgKey = "err.web_publish.not_provisioned"
	// MsgWebPublishServiceUnavailable 站点管理服务暂时不可用（兜底 500）。
	MsgWebPublishServiceUnavailable MsgKey = "err.web_publish.service_unavailable"
)

// init 把 web-publish 站点管理错误译文并入中心 catalog。
func init() {
	Register(map[MsgKey]map[string]string{
		MsgWebPublishNotProvisioned:     {"zh": "企业未开通 web-publish 能力", "en": "Web-publish capability is not provisioned for this organization"},
		MsgWebPublishServiceUnavailable: {"zh": "站点管理服务暂时不可用", "en": "The site management service is temporarily unavailable"},
	})
}
