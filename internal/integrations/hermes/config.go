package hermes

import (
	"errors"
	"fmt"
	"strings"
)

// ConfigInput 是 RenderConfigYAML 的输入。
// 所有字段均必填:ModelName 是 app 当前选择的模型,
// NewAPIURL 是 new-api 内网 URL(不带 /v1),NewAPIToken 是 manager 端创建的 sk-xxx。
type ConfigInput struct {
	ModelName   string
	NewAPIURL   string
	NewAPIToken string
}

// EnvInput 是 RenderEnv 的输入。
// NewAPIURL / NewAPIToken 是 OPENAI_* 凭据;WeixinAccountID / WeixinToken / WeixinBaseURL
// 是扫码 bound 后由 ChannelCheckBindingHandler 传入的 weixin platform 凭据(可选)。
type EnvInput struct {
	NewAPIURL   string
	NewAPIToken string
	// 以下字段为可选,bound 后由 ChannelCheckBindingHandler 传入。
	// 未填时跳过 WEIXIN_* 行写入。
	WeixinAccountID string
	WeixinToken     string
	WeixinBaseURL   string
}

// ErrConfigMissingField ConfigInput 必填字段为空。
var ErrConfigMissingField = errors.New("config: 必填字段为空")

// RenderConfigYAML 渲染 Hermes config.yaml。
// 写入 model.{default,provider,base_url,api_key} + auxiliary 全 main + memory/terminal 默认值。
// 输出可直接写到 apps/<app_id>/.hermes/config.yaml,Hermes 启动时读取。
func RenderConfigYAML(in ConfigInput) (string, error) {
	if strings.TrimSpace(in.ModelName) == "" ||
		strings.TrimSpace(in.NewAPIURL) == "" ||
		strings.TrimSpace(in.NewAPIToken) == "" {
		return "", ErrConfigMissingField
	}
	return fmt.Sprintf(`# Hermes 配置 - 由 oc-manager 在 app_initialize 时生成
# 模型 provider 走本地 new-api(OpenAI 兼容 endpoint)。

model:
  default: %q
  provider: "custom"
  base_url: %q
  api_key: %q

# auxiliary 全部走 main,避免 Hermes 默认去拨 OpenRouter。
auxiliary:
  vision:         { provider: main }
  compression:    { provider: main }
  web_extract:    { provider: main }
  session_search: { provider: main }

memory:
  memory_enabled: true
  user_profile_enabled: true
  memory_char_limit: 2200
  user_char_limit: 1375

terminal:
  backend: "local"
  cwd: "."
  timeout: 180
  lifetime_seconds: 300
`, in.ModelName, in.NewAPIURL+"/v1", in.NewAPIToken), nil
}

// RenderEnv 渲染 Hermes .env 文件内容。
// 固定输出 OPENAI_API_KEY / OPENAI_BASE_URL / GATEWAY_ALLOW_ALL_USERS=true / WEIXIN_DM_POLICY=open。
//
//   - WEIXIN_DM_POLICY=open:weixin platform 必须显式声明的策略,默认拒绝所有未授权 DM
//     ("Unauthorized user"),必须设置 open 才接收用户消息。
//   - GATEWAY_ALLOW_ALL_USERS=true:绕过 Hermes 全局 user pairing 流程。未设置时 Hermes
//     对每个未配对 sender 返回 "Hi~ I don't recognize you yet! Here's your pairing code: ...",
//     要求管理员跑 `hermes pairing approve weixin <code>` — 本地容器化部署没有交互式 CLI,
//     必须用环境变量绕过,否则所有真实用户的首条消息都拿不到模型回复。
//
// 当 WeixinAccountID / WeixinToken 均不为空时,追加 WEIXIN_ACCOUNT_ID/TOKEN/BASE_URL/CDN_BASE_URL。
func RenderEnv(in EnvInput) string {
	s := fmt.Sprintf(
		"OPENAI_API_KEY=%s\nOPENAI_BASE_URL=%s/v1\n\n# 绕过 Hermes user pairing 流程,接受所有未配对 sender(本地部署没有交互 CLI 跑 approve)\nGATEWAY_ALLOW_ALL_USERS=true\n\n# Weixin platform policy (Hermes weixin 默认拒所有 DM,需显式 open)\nWEIXIN_DM_POLICY=open\n",
		in.NewAPIToken, in.NewAPIURL,
	)
	if in.WeixinAccountID != "" && in.WeixinToken != "" {
		baseURL := in.WeixinBaseURL
		if baseURL == "" {
			baseURL = "https://weixin.novac2c.com"
		}
		s += fmt.Sprintf(
			"\n# Weixin 平台凭证,由扫码 bound 时写入\nWEIXIN_ACCOUNT_ID=%s\nWEIXIN_TOKEN=%s\nWEIXIN_BASE_URL=%s\nWEIXIN_CDN_BASE_URL=https://novac2c.cdn.weixin.qq.com/c2c\n",
			in.WeixinAccountID, in.WeixinToken, baseURL,
		)
	}
	return s
}
