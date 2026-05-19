package hermes

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRender(t *testing.T) {
	cases := []struct {
		// name 标识该测试场景。
		name string
		// input 是 Render 的输入。
		input PromptInput
		// wantContains 必须出现在 SOUL.md 中的子串。
		wantContains []string
		// wantOrder 期待的 CompositionOrder。
		wantOrder []string
		// wantErr 是否期待错误(nil = 期待成功)。
		wantErr error
	}{
		{
			// 覆盖三层全填充 + 变量替换的正常路径。
			name: "三层都有 + 变量替换",
			input: PromptInput{
				PlatformPrompt: "平台:{platform_name}",
				OrgPrompt:      "组织:{org_name}",
				AppPrompt:      "应用:{app_name}",
				Variables: map[string]string{
					"platform_name": "oc-manager",
					"org_name":      "test-org",
					"app_name":      "demo",
				},
			},
			wantContains: []string{"平台:oc-manager", "组织:test-org", "应用:demo"},
			wantOrder:    []string{"platform", "organization", "app"},
		},
		{
			// 覆盖某层为空时被跳过,CompositionOrder 不应包含空层。
			name: "组织层为空,被跳过",
			input: PromptInput{
				PlatformPrompt: "平台",
				OrgPrompt:      "",
				AppPrompt:      "应用",
			},
			wantContains: []string{"平台", "应用"},
			wantOrder:    []string{"platform", "app"},
		},
		{
			// 覆盖占位符未被 Variables 覆盖时返回 ErrPromptUnresolvedPlaceholder。
			name: "占位符未替换,返回错误",
			input: PromptInput{
				PlatformPrompt: "平台:{missing}",
				Variables:      map[string]string{},
			},
			wantErr: ErrPromptUnresolvedPlaceholder,
		},
		{
			// 覆盖三层全空时返回 ErrPromptEmpty。
			name:    "三层全空,返回错误",
			input:   PromptInput{},
			wantErr: ErrPromptEmpty,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Render(tc.input)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			for _, sub := range tc.wantContains {
				require.True(t, strings.Contains(got.Prompt, sub),
					"SOUL.md 应包含 %q,实际:%s", sub, got.Prompt)
			}
			require.Equal(t, tc.wantOrder, got.CompositionOrder)
		})
	}
}

// 验证 RenderRuleText 仅做占位符替换，不做层级拼装。
func TestRenderRuleText_ReplacesPlaceholders(t *testing.T) {
	out, err := RenderRuleText("hello {org_name} / {app_name}", map[string]string{
		"org_name": "Acme", "app_name": "Bot", "owner_name": "ada",
	})
	require.NoError(t, err)
	assert.Equal(t, "hello Acme / Bot", out)
}

// 验证 RenderRuleText 在未替换占位符时报 ErrPromptUnresolvedPlaceholder。
func TestRenderRuleText_UnresolvedPlaceholder(t *testing.T) {
	_, err := RenderRuleText("hi {nope}", map[string]string{})
	require.ErrorIs(t, err, ErrPromptUnresolvedPlaceholder)
}

// 验证 RenderPersonaText 同样做占位符替换，且与 RenderRuleText 行为一致。
func TestRenderPersonaText_ReplacesPlaceholders(t *testing.T) {
	out, err := RenderPersonaText("I am {owner_name}", map[string]string{
		"owner_name": "ada",
	})
	require.NoError(t, err)
	assert.Equal(t, "I am ada", out)
}
