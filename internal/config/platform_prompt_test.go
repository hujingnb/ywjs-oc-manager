package config

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/domain"
)

// TestPlatformPrompts_Invariants 校验两类平台提示词各自满足身份、技能和渲染约束。
func TestPlatformPrompts_Invariants(t *testing.T) {
	testCases := []struct {
		name     string
		appType  domain.AppType
		prompt   string
		identity string
		contains []string
		excludes []string
	}{
		// 普通实例必须保留工作目录交付约束，确保文件可被平台浏览和下载。
		{
			name:     "普通实例",
			appType:  domain.AppTypeStandard,
			prompt:   DefaultInstanceSystemPromptTemplate,
			identity: "你是 AiGoWork 智能助手。",
			contains: []string{"## 工作目录约定", "/opt/data/workspace/"},
			excludes: []string{"智能客服", "外部访客"},
		},
		// AICC 面向外部访客，不应混入仅对内部实例有效的工作目录规则。
		{
			name:     "AICC",
			appType:  domain.AppTypeAICC,
			prompt:   DefaultAICCSystemPromptTemplate,
			identity: "你是 AiGoWork 智能客服",
			contains: []string{"智能客服", "外部访客"},
			excludes: []string{"## 工作目录约定", "/opt/data/workspace/"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			// 非空：提示词为空会导致对应 SOUL.md 平台规则缺失。
			require.NotEmpty(t, strings.TrimSpace(testCase.prompt))
			// 选择函数必须按应用类型返回正确提示词。
			assert.Equal(t, testCase.prompt, PlatformPromptForApp(testCase.appType))
			// 身份与内容边界分别锁定，避免两类用户场景互相泄漏规则。
			assert.Contains(t, testCase.prompt, testCase.identity)
			assert.NotContains(t, testCase.prompt, "你是 Hermes 智能助手")
			for _, text := range testCase.contains {
				assert.Contains(t, testCase.prompt, text)
			}
			for _, text := range testCase.excludes {
				assert.NotContains(t, testCase.prompt, text)
			}
			if testCase.appType == domain.AppTypeAICC {
				// AICC 只能使用平台审核且处于当前白名单的客服 Skill，不得回退至通用能力。
				assert.Contains(t, testCase.prompt, "仅可使用平台审核并在当前 AICC 白名单中的客服 Skill")
				assert.Contains(t, testCase.prompt, "不得调用未审核、通用或未在白名单中的 Skill")
				// AICC 的固定工具、安全和响应契约必须固化在 SOUL.md 平台层，避免每轮重复传输。
				assert.Contains(t, testCase.prompt, "aicc_knowledge_search")
				assert.Contains(t, testCase.prompt, "web_search")
				assert.Contains(t, testCase.prompt, "web_extract")
				assert.Contains(t, testCase.prompt, "不得调用或建议调用命令、终端、代码、文件、进程")
				assert.Contains(t, testCase.prompt, "text、sources、next_action、flags")
				assert.Contains(t, testCase.prompt, "aicc_response_sources")
				// AICC 不得被平台提示词强制枚举全部 Skill，也不得在无匹配时自行回退通用能力。
				assert.NotContains(t, testCase.prompt, "处理任何用户任务前，必须先调用 skills_list")
				assert.NotContains(t, testCase.prompt, "只有在没有适用的技能时，才使用通用能力")
				assert.NotContains(t, testCase.prompt, "通用能力完成任务")
			} else {
				// 普通实例保留通用 Skill 发现流程，确保原有工作流不受 AICC 裁剪影响。
				assert.Contains(t, testCase.prompt, "处理任何用户任务前，必须先调用 skills_list")
				assert.Contains(t, testCase.prompt, "适用的技能")
				assert.Contains(t, testCase.prompt, "先阅读该技能的说明")
				assert.Contains(t, testCase.prompt, "严格按其指引完成任务")
				assert.Contains(t, testCase.prompt, "与当前任务无关的技能不用启用")
				assert.Contains(t, testCase.prompt, "没有适用的技能")
				assert.Contains(t, testCase.prompt, "通用能力")
			}
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
		name    string
		appType domain.AppType
		prompt  string
	}{
		// 普通实例的 hash 作为普通应用重渲染判断的版本标识。
		{name: "普通实例", appType: domain.AppTypeStandard, prompt: DefaultInstanceSystemPromptTemplate},
		// AICC 的 hash 必须独立，避免客服规则变更漏掉重渲染。
		{name: "AICC", appType: domain.AppTypeAICC, prompt: DefaultAICCSystemPromptTemplate},
	}

	hashes := make([]string, 0, len(testCases))
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			hash := PlatformPromptHash(testCase.appType)
			// sha256 的十六进制编码固定为 64 位，并且同一场景调用结果稳定。
			require.Len(t, hash, 64)
			require.Equal(t, hash, PlatformPromptHash(testCase.appType))
			sum := sha256.Sum256([]byte(testCase.prompt))
			assert.Equal(t, hex.EncodeToString(sum[:]), hash)
			hashes = append(hashes, hash)
		})
	}

	// 提示词文本不同，故其版本 hash 也必须不同。
	assert.NotEqual(t, hashes[0], hashes[1])
}
