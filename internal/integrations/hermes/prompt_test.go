package hermes

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
