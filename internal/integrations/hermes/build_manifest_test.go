package hermes

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBuildManifestFullFields 验证全字段装配（含 knowledge / routing / skills）。
func TestBuildManifestFullFields(t *testing.T) {
	// 正常路径：knowledge 两字段齐全时写入，skills/routing 透传
	in := AppInputData{
		AppID: "a1", AppName: "demo", Model: "gpt-x",
		OpenAIAPIKey: "sk-xxx", OpenAIBaseURL: "http://new-api:3000",
		KnowledgeRuntimeBaseURL: "http://manager/runtime", KnowledgeAppToken: "tok",
		Routing:       map[string]string{"fast": "gpt-mini"},
		SkillRelPaths: []string{"resources/skills/weather.tar"},
	}
	m := BuildManifest(in)
	assert.Equal(t, "a1", m.App.ID)
	assert.Equal(t, "sk-xxx", m.Credentials.OpenAI.APIKey)
	assert.Equal(t, "resources/persona.md", m.Resources.Persona)
	assert.Equal(t, []string{"resources/skills/weather.tar"}, m.Resources.Skills)
	assert.Equal(t, "tok", m.Knowledge.AppToken)
	assert.Equal(t, "gpt-mini", m.Routing["fast"])
}

// TestBuildManifestOmitsKnowledgeWhenIncomplete 验证 knowledge 字段不全时不写入。
func TestBuildManifestOmitsKnowledgeWhenIncomplete(t *testing.T) {
	// 边界：仅有 base url 缺 token，knowledge 应保持零值（omitempty 省略）
	m := BuildManifest(AppInputData{AppID: "a1", KnowledgeRuntimeBaseURL: "http://x"})
	assert.Empty(t, m.Knowledge.AppToken)
	assert.Empty(t, m.Knowledge.RuntimeBaseURL)
}
