package hermes

import (
	"strings"
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
		require.True(t, strings.Contains(got, sub),
			"config.yaml 应包含 %q,实际:\n%s", sub, got)
	}
}

func TestRenderConfigYAML_缺字段返回错误(t *testing.T) {
	// 覆盖必填字段缺失场景。
	_, err := RenderConfigYAML(ConfigInput{ModelName: "", NewAPIURL: "x", NewAPIToken: "y"})
	require.ErrorIs(t, err, ErrConfigMissingField)
}

func TestRenderEnv(t *testing.T) {
	// 覆盖 .env 渲染:OPENAI_API_KEY/OPENAI_BASE_URL 两行。
	got := RenderEnv(EnvInput{
		NewAPIURL:   "http://new-api:3000",
		NewAPIToken: "sk-abc",
	})
	require.Equal(t, "OPENAI_API_KEY=sk-abc\nOPENAI_BASE_URL=http://new-api:3000/v1\n", got)
}
