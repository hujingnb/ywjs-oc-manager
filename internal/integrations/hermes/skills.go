package hermes

import (
	"errors"
	"fmt"
	"regexp"
)

// SkillScope 表示知识库 skill 的作用域。
type SkillScope string

const (
	// ScopeOrg 组织级 skill,目录前缀 kb-org-。
	ScopeOrg SkillScope = "org"
	// ScopeApp 应用级 skill,目录前缀 kb-app-。
	ScopeApp SkillScope = "app"
)

// KnowledgeDoc 是 RenderKnowledgeSkill 的输入。
// Slug 用作目录与 skill name 的稳定 id,要求小写字母数字加连字符。
// Title 是 SKILL.md frontmatter name 的可读形式(用 Slug 拼接成 name)。
// Summary 进入 frontmatter description,影响 agent 发现 skill。
// Body 是 markdown 正文,直接写入 SKILL.md 主体。
type KnowledgeDoc struct {
	Scope   SkillScope
	Slug    string
	Title   string
	Summary string
	Body    string
}

// SkillRender 是 RenderKnowledgeSkill 的输出。
// DirName 是宿主机/容器内 skills 目录名(不含父路径)。
// SkillMD 是 SKILL.md 完整内容(frontmatter + 正文)。
type SkillRender struct {
	DirName string
	SkillMD string
}

var (
	// ErrInvalidSlug Slug 含非法字符。
	ErrInvalidSlug = errors.New("skills: 非法 slug")
	// ErrInvalidScope Scope 不是 org/app。
	ErrInvalidScope = errors.New("skills: 非法 scope")
)

// slugPattern 限制 slug 仅含小写字母数字与连字符,首尾不能是连字符。
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// RenderKnowledgeSkill 把一份知识库文档渲染成 Hermes skill 内容。
// 调用方拿到 SkillRender 后,自己负责创建宿主机目录 ~/.hermes/skills/<DirName>/
// 并把 SkillMD 写入该目录下的 SKILL.md。
func RenderKnowledgeSkill(d KnowledgeDoc) (SkillRender, error) {
	if d.Scope != ScopeOrg && d.Scope != ScopeApp {
		return SkillRender{}, ErrInvalidScope
	}
	if !slugPattern.MatchString(d.Slug) {
		return SkillRender{}, ErrInvalidSlug
	}

	dir := fmt.Sprintf("kb-%s-%s", d.Scope, d.Slug)
	desc := d.Summary
	if desc == "" {
		desc = d.Title
	}

	skillMD := fmt.Sprintf(`---
name: %s
description: %s
scope: %s
---

# %s

%s
`, dir, desc, d.Scope, d.Title, d.Body)

	return SkillRender{
		DirName: dir,
		SkillMD: skillMD,
	}, nil
}
