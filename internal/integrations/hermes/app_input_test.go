package hermes

import (
	"context"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeInputWriter 记录 WriteAppInput 每次调用的相对路径与内容，
// 便于在测试中验证写入顺序与文件内容是否符合预期。
type fakeInputWriter struct {
	items map[string]string
	order []string
}

// WriteAppInputFile 实现 AppInputWriter 接口，把 body 读到内存以便测试断言。
func (f *fakeInputWriter) WriteAppInputFile(_ context.Context, _ string, relPath string, body io.Reader) error {
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if f.items == nil {
		f.items = map[string]string{}
	}
	f.items[relPath] = string(b)
	f.order = append(f.order, relPath)
	return nil
}

// 验证 WriteAppInput v2：只写 manifest.yaml + persona.md + platform-rules.md；
// 不再写 organization-rules.md / application-rules.md；
// manifest 包含 routing 和 skills；persona 来自版本内置提示词。
func TestWriteAppInput_WritesManifestAndResources(t *testing.T) {
	w := &fakeInputWriter{}
	// v2 AppInputData：无 OrganizationRule / ApplicationRule；有 Routing 和 SkillRelPaths
	in := AppInputData{
		AppID: "app-x", AppName: "X", Model: "m",
		OpenAIAPIKey: "sk-x", OpenAIBaseURL: "http://x",
		KnowledgeRuntimeBaseURL: "http://manager-api:8080",
		KnowledgeAppToken:       "runtime-token",
		PersonaText:             "Hi {owner_name}", // 版本内置提示词，含占位符
		PlatformRule:            "PLT {org_name}",
		Routing:                 map[string]string{"fast": "gpt-4o-mini"},
		SkillRelPaths:           []string{"resources/skills/weather.tar"},
		OrgName:                 "Acme",
		OwnerName:               "ada",
	}
	require.NoError(t, WriteAppInput(context.Background(), w, "app-x", in))

	// v2 只写三个文件：persona + platform-rules + manifest
	keys := make([]string, 0, len(w.items))
	for k := range w.items {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	assert.Equal(t, []string{
		"manifest.yaml",
		"resources/persona.md",
		"resources/platform-rules.md",
	}, keys, "v2 不应写 organization / application rules 文件")

	// persona 占位符已替换为版本提示词中的变量值
	assert.Equal(t, "Hi ada", w.items["resources/persona.md"])
	assert.Equal(t, "PLT Acme", w.items["resources/platform-rules.md"])

	// organization / application rules 文件不应存在
	assert.NotContains(t, w.items, "resources/organization-rules.md", "v2 不写 org rules")
	assert.NotContains(t, w.items, "resources/application-rules.md", "v2 不写 app rules")

	// manifest.yaml 包含 api key、routing 和 skills
	manifestYAML := w.items["manifest.yaml"]
	assert.True(t, strings.Contains(manifestYAML, "sk-x"), "manifest 应包含 api_key")
	assert.Contains(t, manifestYAML, "routing:", "manifest v2 应包含 routing")
	assert.Contains(t, manifestYAML, "skills:", "manifest v2 应包含 skills")
	assert.Contains(t, manifestYAML, "weather.tar", "manifest skills 应包含 weather.tar")
	assert.Contains(t, manifestYAML, "knowledge:", "manifest 应包含知识库 runtime 配置")
	assert.Contains(t, manifestYAML, "runtime_base_url: http://manager-api:8080")
	assert.Contains(t, manifestYAML, "app_token: runtime-token")
	assert.NotContains(t, manifestYAML, "ragflow", "manifest 不应暴露 RAGFlow 凭证或目标")

	// manifest.yaml 是最后一个写入（先 resources 后 manifest，避免中间态）
	assert.Equal(t, "manifest.yaml", w.order[len(w.order)-1])
}

// 验证 WriteAppInput v2：无 routing / skills 时 omitempty 省略这两个字段。
func TestWriteAppInput_NoRoutingOrSkills_OmitEmpty(t *testing.T) {
	w := &fakeInputWriter{}
	// 不传 Routing 和 SkillRelPaths，验证 omitempty 行为
	in := AppInputData{
		AppID: "app-z", AppName: "Z", Model: "m",
		OpenAIAPIKey: "sk-z", OpenAIBaseURL: "http://z",
		PersonaText:  "Hello",
		PlatformRule: "PLT",
		OrgName:      "Org", OwnerName: "user",
	}
	require.NoError(t, WriteAppInput(context.Background(), w, "app-z", in))

	manifestYAML := w.items["manifest.yaml"]
	assert.NotContains(t, manifestYAML, "routing:", "空 routing 应被 omitempty 省略")
	assert.NotContains(t, manifestYAML, "skills:", "空 skills 应被 omitempty 省略")
}
