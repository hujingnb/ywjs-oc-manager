package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultSystemPromptTemplate_Invariants 校验固化的平台层 prompt 常量满足
// 白标与渲染约束：确立 AiGoWork 身份、无 Hermes 品牌残留、保留工作目录约定、
// 不含会被 RenderRuleText 误当占位符的花括号。
func TestDefaultSystemPromptTemplate_Invariants(t *testing.T) {
	tpl := DefaultSystemPromptTemplate

	// 非空：常量是平台层唯一来源，为空会导致 SOUL.md 平台段丢失。
	require.NotEmpty(t, strings.TrimSpace(tpl))

	// 身份钉死为 AiGoWork：被问身份时的对外名称。
	assert.Contains(t, tpl, "你是 AiGoWork 智能助手。")

	// 抑制上游引擎品牌泄漏：不得出现旧的「你是 Hermes 智能助手」身份行，
	// 也不得把 Nous Research 作为可暴露品牌写进来。
	assert.NotContains(t, tpl, "你是 Hermes 智能助手")

	// 保留工作目录约定段：文件交付依赖模型把输出落在 workspace。
	assert.Contains(t, tpl, "## 工作目录约定")
	assert.Contains(t, tpl, "/opt/data/workspace/")

	// 无花括号：RenderRuleText 会把 {var} 当占位符替换，误伤会导致渲染报错。
	assert.NotContains(t, tpl, "{")
	assert.NotContains(t, tpl, "}")
}
