package hermes

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderConfigYAML(t *testing.T) {
	// 覆盖完整字段渲染:provider/base_url/api_key/model + 4 个 auxiliary 全 main。
	got, err := RenderConfigYAML(ConfigInput{
		ModelName:   "qwen3.5:27b",
		NewAPIURL:   "http://new-api:3000",
		NewAPIToken: "sk-test-xxx",
	})
	require.NoError(t, err)
	for _, sub := range []string{
		`default: "qwen3.5:27b"`,
		`provider: "custom"`,
		`base_url: "http://new-api:3000/v1"`,
		`api_key: "sk-test-xxx"`,
		`vision:`,
		`provider: main`,
	} {
		require.Contains(t, got, sub, "config.yaml 应包含 %q", sub)
	}
}

func TestRenderConfigYAML_缺字段返回错误(t *testing.T) {
	// 覆盖必填字段缺失场景。
	_, err := RenderConfigYAML(ConfigInput{ModelName: "", NewAPIURL: "x", NewAPIToken: "y"})
	require.ErrorIs(t, err, ErrConfigMissingField)
}

func TestRenderEnv_基础凭据含DM_POLICY(t *testing.T) {
	// 覆盖 .env 渲染:OPENAI_API_KEY/OPENAI_BASE_URL + GATEWAY_ALLOW_ALL_USERS + WEIXIN_DM_POLICY 必须存在。
	// 无 Weixin 凭证时不应包含 WEIXIN_ACCOUNT_ID。
	got := RenderEnv(EnvInput{
		NewAPIURL:   "http://new-api:3000",
		NewAPIToken: "sk-abc",
	})
	require.Contains(t, got, "OPENAI_API_KEY=sk-abc")
	require.Contains(t, got, "OPENAI_BASE_URL=http://new-api:3000/v1")
	// GATEWAY_ALLOW_ALL_USERS=true 用于绕过 Hermes user pairing,
	// 否则 weixin DM 用户首条消息都被 pairing code 拦住。
	require.Contains(t, got, "GATEWAY_ALLOW_ALL_USERS=true")
	// WEIXIN_DM_POLICY=open 必须始终存在,否则 weixin platform 拒绝所有 DM。
	require.Contains(t, got, "WEIXIN_DM_POLICY=open")
	// 无 weixin 凭证时,不应写入 WEIXIN_ACCOUNT_ID。
	require.NotContains(t, got, "WEIXIN_ACCOUNT_ID")
}

func TestRenderEnv_含Weixin凭证(t *testing.T) {
	// 覆盖有 weixin 凭证时,.env 应同时含 OPENAI_* + WEIXIN_DM_POLICY + WEIXIN_ACCOUNT_ID 等。
	got := RenderEnv(EnvInput{
		NewAPIURL:       "http://new-api:3000",
		NewAPIToken:     "sk-xyz",
		WeixinAccountID: "wx-acc-001",
		WeixinToken:     "wx-tok-secret",
		WeixinBaseURL:   "https://weixin.example.com",
	})
	require.Contains(t, got, "OPENAI_API_KEY=sk-xyz")
	require.Contains(t, got, "OPENAI_BASE_URL=http://new-api:3000/v1")
	require.Contains(t, got, "WEIXIN_DM_POLICY=open")
	require.Contains(t, got, "WEIXIN_ACCOUNT_ID=wx-acc-001")
	require.Contains(t, got, "WEIXIN_TOKEN=wx-tok-secret")
	require.Contains(t, got, "WEIXIN_BASE_URL=https://weixin.example.com")
	require.Contains(t, got, "WEIXIN_CDN_BASE_URL=https://novac2c.cdn.weixin.qq.com/c2c")
}

func TestRenderEnv_Weixin凭证不完整时不写WEIXIN_ACCOUNT_ID(t *testing.T) {
	// 边界:只有 WeixinAccountID 没有 WeixinToken 时,不应写入任何 WEIXIN_ACCOUNT_ID/TOKEN 行。
	got := RenderEnv(EnvInput{
		NewAPIURL:       "http://new-api:3000",
		NewAPIToken:     "sk-abc",
		WeixinAccountID: "wx-acc-001",
		WeixinToken:     "", // token 为空,不应写入 weixin 段
	})
	require.Contains(t, got, "WEIXIN_DM_POLICY=open")
	require.NotContains(t, got, "WEIXIN_ACCOUNT_ID")
}
