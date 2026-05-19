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

// 验证 WriteAppInput 写入 manifest.yaml + 四份 resources markdown；
// rules 文本中的 {org_name} 已被替换为真实值。
func TestWriteAppInput_WritesManifestAndResources(t *testing.T) {
	w := &fakeInputWriter{}
	in := AppInputData{
		AppID: "app-x", AppName: "X", Model: "m",
		OpenAIAPIKey: "sk-x", OpenAIBaseURL: "http://x",
		PersonaText:      "Hi {owner_name}",
		PlatformRule:     "PLT {org_name}",
		OrganizationRule: "ORG",
		ApplicationRule:  "APP {app_name}",
		OrgName:          "Acme",
		OwnerName:        "ada",
	}
	require.NoError(t, WriteAppInput(context.Background(), w, "app-x", in))

	keys := make([]string, 0, len(w.items))
	for k := range w.items {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	assert.Equal(t, []string{
		"manifest.yaml",
		"resources/application-rules.md",
		"resources/organization-rules.md",
		"resources/persona.md",
		"resources/platform-rules.md",
	}, keys)
	assert.Equal(t, "Hi ada", w.items["resources/persona.md"])
	assert.Equal(t, "PLT Acme", w.items["resources/platform-rules.md"])
	assert.Equal(t, "APP X", w.items["resources/application-rules.md"])
	assert.True(t, strings.Contains(w.items["manifest.yaml"], "sk-x"))
	// 验证 manifest.yaml 是最后一个写入（先 resources 后 manifest，避免中间态）
	assert.Equal(t, "manifest.yaml", w.order[len(w.order)-1])
}
