package apierror

// 本文件集中登记「认证/鉴权」（auth）domain handler 层错误文案的 MsgKey 与中/英译文。
// 范围覆盖 internal/api/handlers/auth.go 中内联的静态中文 apierror.New 调用：locale 非法、
// 凭证错误、登录凭证无效、人机验证（缺失/失效）、认证服务不可用、生成验证码失败等。
// zh 译文逐字取自 handler 原中文（标点/空格不改），en 为忠实英译；相同中文复用同一 key
// （「人机验证已失效，请重试」在 CaptchaInvalid/CaptchaReplayed 两处出现，复用同一 key）。
// 动态明细文案（禁用用户/组织走 SafeErrorMessage、成员创建非法走 validationServiceMessage）
// 在 handler 内保留运行期生成，不进 catalog。通用语义不在本文件重复定义。

// 认证/鉴权 domain 静态错误 MsgKey 常量。
const (
	// MsgAuthInvalidLocale 不支持的语言（UpdateLocale 单独文案，与 app domain 的 locale 文案措辞不同）。
	MsgAuthInvalidLocale MsgKey = "err.auth.invalid_locale"
	// MsgAuthInvalidCredentials 用户名或密码错误，也可能是未填写组织标识。
	MsgAuthInvalidCredentials MsgKey = "err.auth.invalid_credentials"
	// MsgAuthInvalidToken 登录凭证无效。
	MsgAuthInvalidToken MsgKey = "err.auth.invalid_token"
	// MsgAuthCaptchaRequired 请先完成人机验证。
	MsgAuthCaptchaRequired MsgKey = "err.auth.captcha_required"
	// MsgAuthCaptchaExpired 人机验证已失效，请重试；CaptchaInvalid/CaptchaReplayed 两处复用。
	MsgAuthCaptchaExpired MsgKey = "err.auth.captcha_expired"
	// MsgAuthServiceUnavailable 认证服务暂时不可用。
	MsgAuthServiceUnavailable MsgKey = "err.auth.service_unavailable"
	// MsgAuthCaptchaChallengeFailed 生成人机验证失败。
	MsgAuthCaptchaChallengeFailed MsgKey = "err.auth.captcha_challenge_failed"
	// MsgAuthMissingToken 缺少访问令牌（RequireUserAuth 中间件：Authorization header 缺失/非 Bearer/空 token）。
	MsgAuthMissingToken MsgKey = "err.auth.missing_token"
	// MsgAuthAccessTokenInvalid 访问令牌无效（RequireUserAuth 中间件：签名错/过期等统一兜底，不暴露具体原因）。
	MsgAuthAccessTokenInvalid MsgKey = "err.auth.access_token_invalid"
	// MsgAuthCSRFInvalid CSRF token 校验失败（RequireCSRF 中间件：double-submit cookie 与 header 不一致）。
	MsgAuthCSRFInvalid MsgKey = "err.auth.csrf_invalid"
)

// init 把认证/鉴权 domain 错误译文并入中心 catalog。
func init() {
	Register(map[MsgKey]map[string]string{
		MsgAuthInvalidLocale:          {"zh": "不支持的语言", "en": "Unsupported language"},
		MsgAuthInvalidCredentials:     {"zh": "用户名或密码错误，也可能是未填写组织标识", "en": "Incorrect username or password, or the organization identifier may be missing"},
		MsgAuthInvalidToken:           {"zh": "登录凭证无效", "en": "Invalid login credential"},
		MsgAuthCaptchaRequired:        {"zh": "请先完成人机验证", "en": "Please complete the human verification first"},
		MsgAuthCaptchaExpired:         {"zh": "人机验证已失效，请重试", "en": "Human verification has expired. Please try again"},
		MsgAuthServiceUnavailable:     {"zh": "认证服务暂时不可用", "en": "The authentication service is temporarily unavailable"},
		MsgAuthCaptchaChallengeFailed: {"zh": "生成人机验证失败", "en": "Failed to generate human verification"},
		MsgAuthMissingToken:           {"zh": "缺少访问令牌", "en": "Missing access token"},
		MsgAuthAccessTokenInvalid:     {"zh": "访问令牌无效", "en": "Invalid access token"},
		MsgAuthCSRFInvalid:            {"zh": "CSRF token 校验失败", "en": "CSRF token validation failed"},
	})
}
