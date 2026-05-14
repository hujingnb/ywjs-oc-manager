package hermes

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderKnowledgeSkill(t *testing.T) {
	// 覆盖标准 skill 渲染:frontmatter 含 name/description/scope,正文为知识库内容。
	got, err := RenderKnowledgeSkill(KnowledgeDoc{
		Scope:   ScopeOrg,
		Slug:    "billing-rules",
		Title:   "计费规则",
		Summary: "组织内部的计费规则汇总",
		Body:    "## 规则一\n月度结算。",
	})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(got.SkillMD, "---\n"))
	require.True(t, strings.Contains(got.SkillMD, "name: kb-org-billing-rules"))
	require.True(t, strings.Contains(got.SkillMD, "description: 组织内部的计费规则汇总"))
	require.True(t, strings.Contains(got.SkillMD, "## 规则一"))
	require.Equal(t, "kb-org-billing-rules", got.DirName)
}

func TestRenderKnowledgeSkill_Slug非法返回错误(t *testing.T) {
	// 覆盖 slug 含非法字符场景。
	_, err := RenderKnowledgeSkill(KnowledgeDoc{
		Scope: ScopeApp,
		Slug:  "has space",
		Title: "x",
		Body:  "y",
	})
	require.ErrorIs(t, err, ErrInvalidSlug)
}

func TestRenderKnowledgeSkill_Scope非法返回错误(t *testing.T) {
	// 覆盖未知 scope 场景。
	_, err := RenderKnowledgeSkill(KnowledgeDoc{
		Scope: "bad",
		Slug:  "a",
		Title: "t",
		Body:  "b",
	})
	require.ErrorIs(t, err, ErrInvalidScope)
}
