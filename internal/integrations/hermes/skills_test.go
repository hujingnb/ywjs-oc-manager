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

// TestSlugifyKnowledgePath 覆盖知识库相对路径 → 合法 slug 的常见映射,
// 保证 app_initialize 在容器启动前批量生成 .hermes/skills/kb-*-<slug>/ 时
// 不会因路径含大写/扩展名/子目录/中文/标点等而失败。
func TestSlugifyKnowledgePath(t *testing.T) {
	cases := []struct {
		rel      string // 输入相对路径(主副本里的 toSlash 形态)
		want     string // 期望 slug
		wantHash bool   // 若 true,则不强校验 want,只校验是 fallback 形态
	}{
		// 单文件、小写、英文 → 去扩展名即可。
		{rel: "billing-rules.md", want: "billing-rules"},
		// 含子目录:用 '-' 拼接,保留可读性。
		{rel: "policies/refund.md", want: "policies-refund"},
		// 大写 + 标点 + 空格 → 折叠成单 '-'。
		{rel: "FAQ_v2 Final.md", want: "faq-v2-final"},
		// 纯中文文件名 → 走 sha256 fallback。
		{rel: "策略文件.md", wantHash: true},
		// 无扩展名;首尾分隔符要去掉。
		{rel: "-leading-and-trailing-", want: "leading-and-trailing"},
		// 空字符串 → fallback。
		{rel: "", wantHash: true},
	}
	for _, c := range cases {
		got := SlugifyKnowledgePath(c.rel)
		require.True(t, slugPattern.MatchString(got), "slug %q 不符合规则: src=%q", got, c.rel)
		if c.wantHash {
			require.True(t, strings.HasPrefix(got, "kb-"), "应使用 fallback 前缀: src=%q got=%q", c.rel, got)
			continue
		}
		require.Equal(t, c.want, got, "src=%q", c.rel)
	}
}
