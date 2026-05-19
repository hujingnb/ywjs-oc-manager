package hermes

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// 验证 MarshalManifestYAML 字段顺序稳定且 decode 等价。
func TestMarshalManifestYAML_StableShape(t *testing.T) {
	m := Manifest{
		App: ManifestApp{ID: "app-x", Name: "X", Model: "claude-3.7-sonnet"},
		Credentials: ManifestCredentials{
			OpenAI: ManifestOpenAI{APIKey: "sk-x", BaseURL: "http://new-api:3000"},
		},
		Resources: ManifestResources{
			Persona: "resources/persona.md",
			Rules: ManifestRules{
				Platform:     "resources/platform-rules.md",
				Organization: "resources/organization-rules.md",
				Application:  "resources/application-rules.md",
			},
		},
	}

	b, err := MarshalManifestYAML(m)
	require.NoError(t, err)

	var back Manifest
	require.NoError(t, yaml.Unmarshal(b, &back))
	assert.Equal(t, m, back)
}
