package hermes

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// PromptInput 是 Render 的输入，与 legacy OpenClaw 时代签名保持一致以便迁移。
// 平台 / 组织 / 应用三层 prompt 任一为空时跳过该层;Variables 覆盖占位符 {var}。
type PromptInput struct {
	PlatformPrompt string
	OrgPrompt      string
	AppPrompt      string
	Variables      map[string]string
}

// PromptResult 是 Render 的输出。
// Prompt 是渲染后的完整 SOUL.md 文档内容(markdown 文本)。
// CompositionOrder 记录实际拼接的层级,空层不计。
type PromptResult struct {
	Prompt           string   `json:"prompt"`
	CompositionOrder []string `json:"composition_order"`
}

// 渲染错误。
var (
	// ErrPromptUnresolvedPlaceholder 当 Variables 未覆盖模板中的某个 {var} 时返回。
	ErrPromptUnresolvedPlaceholder = errors.New("prompt 仍存在未替换的占位符")
	// ErrPromptEmpty 三层 prompt 全为空。
	ErrPromptEmpty = errors.New("prompt 三层全部为空")
)

// 占位符匹配 {var};变量名仅允许字母数字下划线。
var placeholderPattern = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Render 按 platform → organization → app 顺序拼接,返回 Hermes SOUL.md 内容。
// 函数签名与 legacy openclaw 包的 Render 保持一致以方便迁移，但产出格式已从
// config patch 字符串改为 markdown 文档（适用于 Hermes 直接写入 ~/.hermes/SOUL.md）。
func Render(input PromptInput) (PromptResult, error) {
	layers := []struct {
		key   string
		title string
		value string
	}{
		{"platform", "平台层", input.PlatformPrompt},
		{"organization", "组织层", input.OrgPrompt},
		{"app", "应用层", input.AppPrompt},
	}

	var b strings.Builder
	order := make([]string, 0, len(layers))
	for _, l := range layers {
		if strings.TrimSpace(l.value) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "## %s\n\n%s", l.title, l.value)
		order = append(order, l.key)
	}
	if len(order) == 0 {
		return PromptResult{}, ErrPromptEmpty
	}

	rendered, err := replacePlaceholders(b.String(), input.Variables)
	if err != nil {
		return PromptResult{}, err
	}

	header := "# Agent Identity (SOUL.md)\n\n本文件由 oc-manager 在 app_initialize 时生成,Hermes 启动后注入到 system prompt。\n\n" +
		"## 语言要求\n\n" +
		"始终用简体中文回复用户。即使用户用英文或其他语言提问,也请用中文作答 " +
		"(代码、命令、API 名称、错误码等技术标识保留英文原文)。\n\n"
	return PromptResult{
		Prompt:           header + rendered,
		CompositionOrder: order,
	}, nil
}

// replacePlaceholders 用 Variables 替换 {var} 占位符,任一未替换则返回错误。
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

// VariablesFromContext 给三层 prompt 提供常用变量字典。
// 与 legacy openclaw 包同名同语义,迁移调用方仅需改 import path。
func VariablesFromContext(orgName, appName, ownerName string) map[string]string {
	return map[string]string{
		"org_name":   orgName,
		"app_name":   appName,
		"owner_name": ownerName,
	}
}
