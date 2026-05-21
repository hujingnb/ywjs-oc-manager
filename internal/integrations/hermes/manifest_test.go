package hermes

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// 验证 MarshalManifestYAML 基础字段稳定且 decode 等价；
// 覆盖 manifest v2：仅含 platform 规则，无 routing / skills。
func TestMarshalManifestYAML_StableShape(t *testing.T) {
	// 基础 manifest：无 routing / skills，omitempty 应省略这两个字段
	m := Manifest{
		App: ManifestApp{ID: "app-x", Name: "X", Model: "claude-3.7-sonnet"},
		Credentials: ManifestCredentials{
			OpenAI: ManifestOpenAI{APIKey: "sk-x", BaseURL: "http://new-api:3000"},
		},
		Resources: ManifestResources{
			Persona: "resources/persona.md",
			Rules: ManifestRules{
				Platform: "resources/platform-rules.md",
			},
		},
	}

	b, err := MarshalManifestYAML(m)
	require.NoError(t, err)

	// decode 后等价
	var back Manifest
	require.NoError(t, yaml.Unmarshal(b, &back))
	assert.Equal(t, m, back)

	// routing / skills 字段因 omitempty 不应出现在 yaml 中
	yaml_str := string(b)
	assert.NotContains(t, yaml_str, "routing:", "空 routing 应被 omitempty 省略")
	assert.NotContains(t, yaml_str, "skills:", "空 skills 应被 omitempty 省略")
}

// 验证 manifest v2 携带 routing + skills 时正确序列化并可反序列化。
func TestMarshalManifestYAML_WithRoutingAndSkills(t *testing.T) {
	// 含 routing 和 skills 的完整 manifest v2
	m := Manifest{
		App: ManifestApp{ID: "app-y", Name: "Y", Model: "gpt-4o"},
		Credentials: ManifestCredentials{
			OpenAI: ManifestOpenAI{APIKey: "sk-y", BaseURL: "http://new-api:3000"},
		},
		Resources: ManifestResources{
			Persona: "resources/persona.md",
			Rules: ManifestRules{
				Platform: "resources/platform-rules.md",
			},
			Skills: []string{"resources/skills/weather.tar", "resources/skills/search.tar"},
		},
		Routing: map[string]string{
			"fast":  "gpt-4o-mini",
			"smart": "gpt-4o",
		},
	}

	b, err := MarshalManifestYAML(m)
	require.NoError(t, err)

	// decode 后等价（完整往返）
	var back Manifest
	require.NoError(t, yaml.Unmarshal(b, &back))
	assert.Equal(t, m, back)

	// routing: 和 skills: 必须出现在序列化结果中
	yaml_str := string(b)
	assert.Contains(t, yaml_str, "routing:", "非空 routing 应输出到 yaml")
	assert.Contains(t, yaml_str, "skills:", "非空 skills 应输出到 yaml")
	assert.Contains(t, yaml_str, "weather.tar", "skills 列表应包含 weather.tar")

	// v2 规则中不应出现 organization / application 字段
	assert.NotContains(t, yaml_str, "organization:", "v2 manifest 不含 organization 规则")
	assert.NotContains(t, yaml_str, "application:", "v2 manifest 不含 application 规则")
}
