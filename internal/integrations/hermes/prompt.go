package hermes

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrPromptUnresolvedPlaceholder 当占位符 {var} 未被传入的 vars 覆盖时返回。
// 调用方需中止写入流程，避免把带 `{xxx}` 字面量的 prompt 落到节点 input/resources。
var ErrPromptUnresolvedPlaceholder = errors.New("prompt 仍存在未替换的占位符")

// placeholderPattern 匹配 {var} 形式的占位符；变量名仅允许字母数字下划线，
// 与 manifest / resources 渲染器约定一致。
var placeholderPattern = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// replacePlaceholders 用 vars 替换 in 中的 {var} 占位符；任一未覆盖即聚合后报错。
func replacePlaceholders(in string, vars map[string]string) (string, error) {
	missing := make([]string, 0)
	out := placeholderPattern.ReplaceAllStringFunc(in, func(match string) string {
		name := match[1 : len(match)-1]
		v, ok := vars[name]
		if !ok {
			missing = append(missing, name)
			return match
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("%w: %s", ErrPromptUnresolvedPlaceholder, strings.Join(missing, ","))
	}
	return out, nil
}

// VariablesFromContext 返回三层 rule / persona 渲染时常用的变量字典。
// 命名沿用 legacy openclaw 包语义，便于跨包迁移调用方仅改 import path。
func VariablesFromContext(orgName, appName, ownerName string) map[string]string {
	return map[string]string{
		"org_name":   orgName,
		"app_name":   appName,
		"owner_name": ownerName,
	}
}

// RenderRuleText 替换 rule 文本中的 {var} 占位符。
// vars 未覆盖任一占位符即返回 ErrPromptUnresolvedPlaceholder，调用方应中止写入。
func RenderRuleText(body string, vars map[string]string) (string, error) {
	return replacePlaceholders(body, vars)
}

// RenderPersonaText 行为同 RenderRuleText，单独命名是为了在调用点语义清晰。
func RenderPersonaText(body string, vars map[string]string) (string, error) {
	return replacePlaceholders(body, vars)
}
