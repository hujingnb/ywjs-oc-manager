package config

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPlatformPrompts_Invariants 校验两类平台提示词各自满足身份、技能和渲染约束。
func TestPlatformPrompts_Invariants(t *testing.T) {
	testCases := []struct {
		name       string
		aiccHidden bool
		prompt     string
		identity   string
		contains   []string
		excludes   []string
	}{
		// 普通实例必须保留工作目录交付约束，确保文件可被平台浏览和下载。
		{
			name:       "普通实例",
			aiccHidden: false,
			prompt:     DefaultInstanceSystemPromptTemplate,
			identity:   "你是 AiGoWork 智能助手。",
			contains:   []string{"## 工作目录约定", "/opt/data/workspace/"},
			excludes:   []string{"智能客服", "外部访客"},
		},
		// AICC 面向外部访客，不应混入仅对内部实例有效的工作目录规则。
		{
			name:       "AICC",
			aiccHidden: true,
			prompt:     DefaultAICCSystemPromptTemplate,
			identity:   "你是 AiGoWork 智能客服",
			contains:   []string{"智能客服", "外部访客"},
			excludes:   []string{"## 工作目录约定", "/opt/data/workspace/"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			// 非空：提示词为空会导致对应 SOUL.md 平台规则缺失。
			require.NotEmpty(t, strings.TrimSpace(testCase.prompt))
			// 选择函数必须按 AICC 隐藏标记返回正确提示词。
			assert.Equal(t, testCase.prompt, PlatformPromptForApp(testCase.aiccHidden))
			// 身份与内容边界分别锁定，避免两类用户场景互相泄漏规则。
			assert.Contains(t, testCase.prompt, testCase.identity)
			assert.NotContains(t, testCase.prompt, "你是 Hermes 智能助手")
			for _, text := range testCase.contains {
				assert.Contains(t, testCase.prompt, text)
			}
			for _, text := range testCase.excludes {
				assert.NotContains(t, testCase.prompt, text)
			}
			// 两种对话都必须在适用时遵循已安装技能，且仅无适用技能时回退通用能力。
			assert.Contains(t, testCase.prompt, "处理任何用户任务前，必须先调用 skills_list")
			assert.Contains(t, testCase.prompt, "适用的技能")
			assert.Contains(t, testCase.prompt, "先阅读该技能的说明")
			assert.Contains(t, testCase.prompt, "严格按其指引完成任务")
			assert.Contains(t, testCase.prompt, "与当前任务无关的技能不用启用")
			assert.Contains(t, testCase.prompt, "没有适用的技能")
			assert.Contains(t, testCase.prompt, "通用能力")
			// 花括号会被 RenderRuleText 当作变量占位符，平台提示词中禁止出现。
			assert.NotContains(t, testCase.prompt, "{")
			assert.NotContains(t, testCase.prompt, "}")
		})
	}

	// 两个场景的文本必须不同，才能避免 AICC 继承普通实例的内部交付规则。
	assert.NotEqual(t, DefaultInstanceSystemPromptTemplate, DefaultAICCSystemPromptTemplate)
}

// TestPlatformPromptHash 校验两类平台提示词的 hash 均与所选提示词的 sha256 严格一致。
func TestPlatformPromptHash(t *testing.T) {
	testCases := []struct {
		name       string
		aiccHidden bool
		prompt     string
	}{
		// 普通实例的 hash 作为普通应用重渲染判断的版本标识。
		{name: "普通实例", aiccHidden: false, prompt: DefaultInstanceSystemPromptTemplate},
		// AICC 的 hash 必须独立，避免客服规则变更漏掉重渲染。
		{name: "AICC", aiccHidden: true, prompt: DefaultAICCSystemPromptTemplate},
	}

	hashes := make([]string, 0, len(testCases))
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			hash := PlatformPromptHash(testCase.aiccHidden)
			// sha256 的十六进制编码固定为 64 位，并且同一场景调用结果稳定。
			require.Len(t, hash, 64)
			require.Equal(t, hash, PlatformPromptHash(testCase.aiccHidden))
			sum := sha256.Sum256([]byte(testCase.prompt))
			assert.Equal(t, hex.EncodeToString(sum[:]), hash)
			hashes = append(hashes, hash)
		})
	}

	// 提示词文本不同，故其版本 hash 也必须不同。
	assert.NotEqual(t, hashes[0], hashes[1])
}
