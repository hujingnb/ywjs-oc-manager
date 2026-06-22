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

// TestBuildManifestLanguage 验证 Language 字段透传到 manifest.app.language。
func TestBuildManifestLanguage(t *testing.T) {
	// 正常路径：Language 非空时写入 app.language。
	m := BuildManifest(AppInputData{AppID: "a1", Language: "zh"})
	assert.Equal(t, "zh", m.App.Language, "manifest.app.language 应等于 AppInputData.Language")

	// 边界路径：Language 为空时 app.language 应为空（omitempty 省略序列化）。
	m2 := BuildManifest(AppInputData{AppID: "a2"})
	assert.Empty(t, m2.App.Language, "Language 未设置时 manifest.app.language 应为空")
}

// TestBuildManifestLanguageYAML 验证 Language 非空时序列化到 YAML 包含 language 键。
func TestBuildManifestLanguageYAML(t *testing.T) {
	// Language 字段应出现在序列化后的 YAML 中，且 omitempty 对空字符串生效。
	m := BuildManifest(AppInputData{AppID: "a1", Language: "en"})
	yamlBytes, err := MarshalManifestYAML(m)
	assert.NoError(t, err)
	assert.Contains(t, string(yamlBytes), "language: en", "YAML 应包含 language 字段")

	// 空 Language 时 YAML 不应含 language 键（omitempty）。
	m2 := BuildManifest(AppInputData{AppID: "a2"})
	yamlBytes2, err := MarshalManifestYAML(m2)
	assert.NoError(t, err)
	assert.NotContains(t, string(yamlBytes2), "language:", "空 Language 时 YAML 不应含 language 键")
}
