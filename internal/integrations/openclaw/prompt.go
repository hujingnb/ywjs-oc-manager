// Package openclaw 提供 manager 与 OpenClaw runtime 镜像之间的协议封装。
//
// 当前文件维护 OpenClaw prompt 模板的渲染：将平台、组织、应用三层 prompt 按既定顺序拼接，
// 并把上下文变量替换进模板。任何未替换占位符都会返回错误，避免把 {variable} 字符串带入容器。
package openclaw

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// PromptInput 是渲染时需要的所有上下文。
// 所有字段都有可能为空，渲染时会自动跳过为空的层；但变量必须 cover 模板中所有占位符。
type PromptInput struct {
	PlatformPrompt string
	OrgPrompt      string
	AppPrompt      string
	Variables      map[string]string
}

// PromptResult 描述渲染产物。
// CompositionOrder 给出实际拼接顺序，便于审计或前端展示。
type PromptResult struct {
	Prompt           string   `json:"prompt"`
	CompositionOrder []string `json:"composition_order"`
}

// 与 prompt 渲染相关的错误。
var (
	ErrPromptUnresolvedPlaceholder = errors.New("prompt 仍存在未替换的占位符")
	ErrPromptEmpty                 = errors.New("prompt 三层全部为空")
)

// 占位符匹配 {var}，变量名仅允许字母数字下划线。
var placeholderPattern = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Render 按 platform → org → app 顺序拼接，并替换 {var} 占位符。
// 任意一层为空会被跳过；CompositionOrder 仅记录非空的层；
// 替换完成后若仍有未匹配占位符则返回 ErrPromptUnresolvedPlaceholder。
func Render(input PromptInput) (PromptResult, error) {
	layers := []struct {
		key   string
		value string
	}{
		{"platform", input.PlatformPrompt},
		{"organization", input.OrgPrompt},
		{"app", input.AppPrompt},
	}
	parts := make([]string, 0, 3)
	order := make([]string, 0, 3)
	for _, layer := range layers {
		if strings.TrimSpace(layer.value) == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(layer.value))
		order = append(order, layer.key)
	}
	if len(parts) == 0 {
		return PromptResult{}, ErrPromptEmpty
	}
	combined := strings.Join(parts, "\n\n")

	rendered := placeholderPattern.ReplaceAllStringFunc(combined, func(match string) string {
		name := match[1 : len(match)-1]
		if value, ok := input.Variables[name]; ok {
			return value
		}
		return match
	})

	if remaining := placeholderPattern.FindAllString(rendered, -1); len(remaining) > 0 {
		return PromptResult{}, fmt.Errorf("%w: %s", ErrPromptUnresolvedPlaceholder, strings.Join(remaining, ", "))
	}
	return PromptResult{Prompt: rendered, CompositionOrder: order}, nil
}

// VariablesFromContext 是一个便利构造器，把常见上下文字段塞进变量表。
// 调用方可以直接构造 map，但通过函数能避免分散的字符串字面量。
func VariablesFromContext(orgName, appName, ownerName string) map[string]string {
	return map[string]string{
		"org_name":   orgName,
		"app_name":   appName,
		"owner_name": ownerName,
	}
}
