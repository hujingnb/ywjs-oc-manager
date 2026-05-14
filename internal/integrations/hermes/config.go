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

// EnvInput 是 RenderEnv 的输入,字段同 ConfigInput 子集。
type EnvInput struct {
	NewAPIURL   string
	NewAPIToken string
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
// 只放 OPENAI_API_KEY / OPENAI_BASE_URL,作为 auxiliary.provider=main 的兜底凭据。
// WEIXIN_* 凭证由扫码 runner 在登录成功后追加(不在此处)。
func RenderEnv(in EnvInput) string {
	return fmt.Sprintf("OPENAI_API_KEY=%s\nOPENAI_BASE_URL=%s/v1\n", in.NewAPIToken, in.NewAPIURL)
}
