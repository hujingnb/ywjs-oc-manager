package openclaw

import (
	"errors"
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
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if !strings.HasPrefix(got.Prompt, "你是 OpenClaw 助手") {
		t.Fatalf("prompt should start with platform layer, got %q", got.Prompt)
	}
	if !strings.Contains(got.Prompt, "测试组织 公司助手") {
		t.Fatalf("org layer not substituted: %q", got.Prompt)
	}
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
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if got.Prompt != "你是 测试组织 的客服" {
		t.Fatalf("prompt = %q", got.Prompt)
	}
	if len(got.CompositionOrder) != 1 || got.CompositionOrder[0] != "organization" {
		t.Fatalf("order = %+v", got.CompositionOrder)
	}
}

func TestRenderRejectsUnresolvedPlaceholders(t *testing.T) {
	_, err := Render(PromptInput{
		AppPrompt: "你好 {missing_var}",
	})
	if !errors.Is(err, ErrPromptUnresolvedPlaceholder) {
		t.Fatalf("error = %v, want ErrPromptUnresolvedPlaceholder", err)
	}
	if !strings.Contains(err.Error(), "{missing_var}") {
		t.Fatalf("error should reference missing variable: %v", err)
	}
}

func TestRenderRejectsEmptyInput(t *testing.T) {
	_, err := Render(PromptInput{})
	if !errors.Is(err, ErrPromptEmpty) {
		t.Fatalf("error = %v, want ErrPromptEmpty", err)
	}
}

func TestVariablesFromContextHasExpectedKeys(t *testing.T) {
	got := VariablesFromContext("测试组织", "alice-bot", "Alice")
	if got["org_name"] != "测试组织" || got["app_name"] != "alice-bot" || got["owner_name"] != "Alice" {
		t.Fatalf("variables = %+v", got)
	}
}
