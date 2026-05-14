package hermes

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
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

// BuildKnowledgeSummary 拼出一段供 SKILL.md frontmatter description 使用的
// 业务化引导文案,告诉 agent 何时应主动装载该 skill。
//
// Hermes 在每次轮次会把所有 skill 的 description 打成一个索引塞进 system prompt,
// agent 用语义匹配决定是否调 /kb-* 加载主体。如果 description 只是文件名
// (例如 "greeting.md"),agent 无法判断何时该用,知识库相当于失效。
//
// 这里采取的策略:
//   - 显式说明 scope(组织 / 应用)与可能的覆盖关系(应用级覆盖组织级);
//   - 把首行 markdown 标题 / 文件名作为可读 hint;
//   - 用"用户咨询相关问题时,优先读取本 skill"这类指令性短语
//     强引导 agent 在用户提问时主动加载。
//
// body 为 SKILL.md 主体(用户上传的 markdown 内容);relPath 为主副本相对路径,
// fallback 用于 body 无标题时。
func BuildKnowledgeSummary(scope SkillScope, relPath, body string) string {
	title := extractFirstHeading(body)
	if title == "" {
		title = relPath
	}
	switch scope {
	case ScopeOrg:
		// 组织级:agent 默认参考;若同名 app 级存在,会被覆盖。
		return fmt.Sprintf(
			"组织级知识库文件 %s。介绍本组织业务、产品、政策、规则等权威信息。当用户的提问涉及组织业务、公司、产品、规则、政策、流程时,必须读取本 skill 获取最新内容,不要根据通用知识猜测。",
			title,
		)
	case ScopeApp:
		// 应用级:优先级高于同名组织级 skill。
		return fmt.Sprintf(
			"应用级知识库文件 %s。包含本应用专属规则、话术、配置,优先级高于同名组织级知识。用户的任意提问都应先读取本 skill 确认是否有匹配规则;有则按本 skill 内容回答,无则回退到组织级或通用知识。",
			title,
		)
	}
	return title
}

// extractFirstHeading 抠出 markdown body 首个 # 开头行的标题文本(去 # 与空格)。
// 若 body 无 markdown 标题,返回空串,调用方应回落到 relPath。
func extractFirstHeading(body string) string {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			return strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		}
	}
	return ""
}

// SlugifyKnowledgePath 把知识库相对路径(可能含中文、空格、目录)
// 规整成符合 slugPattern 的合法 slug。
//
// 规则:
//  1. 去掉文件扩展名;
//  2. 把路径分隔符 / 大写字母 / 非法字符全替换成 '-';
//  3. 折叠连续 '-' 并去掉首尾 '-';
//  4. 若处理后为空(纯中文 / 纯标点),回落到 "kb-<sha256(原 rel)[0:12]>"。
//
// 此函数与 RenderKnowledgeSkill 配套,保证 manager 端写入 .hermes/skills/
// 的目录名一定合法;Hermes 容器内 skill loader 能稳定识别。
func SlugifyKnowledgePath(rel string) string {
	if rel == "" {
		return slugFallback(rel)
	}
	// path.Ext 处理 '/' 分隔,与 manager 主副本 ToSlash 一致。
	base := strings.TrimSuffix(rel, path.Ext(rel))
	var b strings.Builder
	b.Grow(len(base))
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
		default:
			b.WriteByte('-')
		}
	}
	s := b.String()
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	if s == "" || !slugPattern.MatchString(s) {
		return slugFallback(rel)
	}
	return s
}

// slugFallback 在 SlugifyKnowledgePath 无法从原路径提取合法字符时,
// 用稳定的 sha256 短哈希兜底,保证两次启动针对同一文件生成同一 slug。
func slugFallback(rel string) string {
	sum := sha256.Sum256([]byte(rel))
	return "kb-" + hex.EncodeToString(sum[:6])
}
