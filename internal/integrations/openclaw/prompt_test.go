// Package openclaw 的 prompt_test 覆盖提示词拼接顺序、变量替换和缺失占位符校验。
package openclaw

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestRenderConcatenatesInOrder(t *testing.T) {
	got, err := Render(PromptInput{
		PlatformPrompt: "你是 OpenClaw 助手",
		OrgPrompt:      "{org_name} 公司助手",
		AppPrompt:      "{app_name} 个人风格",
		Variables:      map[string]string{"org_name": "测试组织", "app_name": "alice-bot"},
	})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(got.Prompt, "你是 OpenClaw 助手"))
	require.True(t, strings.Contains(got.Prompt, "测试组织 公司助手"))
	if got.CompositionOrder[0] != "platform" || got.CompositionOrder[2] != "app" {
		t.Fatalf("order = %+v", got.CompositionOrder)
	}
}

func TestRenderSkipsEmptyLayers(t *testing.T) {
	got, err := Render(PromptInput{
		PlatformPrompt: "",
		OrgPrompt:      "你是 {org_name} 的客服",
		AppPrompt:      "",
		Variables:      map[string]string{"org_name": "测试组织"},
	})
	require.NoError(t, err)
	require.Equal(t, "你是 测试组织 的客服", got.Prompt)
	if len(got.CompositionOrder) != 1 || got.CompositionOrder[0] != "organization" {
		t.Fatalf("order = %+v", got.CompositionOrder)
	}
}

func TestRenderRejectsUnresolvedPlaceholders(t *testing.T) {
	_, err := Render(PromptInput{
		AppPrompt: "你好 {missing_var}",
	})
	require.ErrorIs(t, err, ErrPromptUnresolvedPlaceholder)
	require.True(t, strings.Contains(err.Error(), "{missing_var}"))
}

func TestRenderRejectsEmptyInput(t *testing.T) {
	_, err := Render(PromptInput{})
	require.ErrorIs(t, err, ErrPromptEmpty)
}

func TestVariablesFromContextHasExpectedKeys(t *testing.T) {
	got := VariablesFromContext("测试组织", "alice-bot", "Alice")
	if got["org_name"] != "测试组织" || got["app_name"] != "alice-bot" || got["owner_name"] != "Alice" {
		t.Fatalf("variables = %+v", got)
	}
}
