package hermes

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// skill tar 校验失败的哨兵错误。
var (
	// ErrSkillArchiveNoSkillMD tar 内不含 SKILL.md。
	ErrSkillArchiveNoSkillMD = errors.New("skill tar 内未找到 SKILL.md")
	// ErrSkillArchiveNoName SKILL.md frontmatter 缺少 name 字段。
	ErrSkillArchiveNoName = errors.New("SKILL.md frontmatter 缺少 name")
	// ErrSkillArchiveUnsafePath tar 条目路径越界（含 .. 或绝对路径）。
	ErrSkillArchiveUnsafePath = errors.New("skill tar 含越界路径条目")
	// ErrSkillArchiveBadLayout tar 内 SKILL.md 不在唯一顶层技能目录下。
	// 容器端 render_skills.py 按 <技能名>/SKILL.md 结构解压，根级或更深层级的
	// SKILL.md 会导致渲染异常，因此在上传阶段一律拒绝。
	ErrSkillArchiveBadLayout = errors.New("skill tar 内 SKILL.md 必须位于顶层技能目录内（<技能名>/SKILL.md）")
)

// SkillArchiveInfo 是 skill tar 校验后的元信息。
type SkillArchiveInfo struct {
	// Name 来自 tar 内 SKILL.md frontmatter 的 name 字段。
	Name string
}

// skillMDFrontmatter 仅取 SKILL.md frontmatter 需要的字段。
type skillMDFrontmatter struct {
	Name string `yaml:"name"`
}

// InspectSkillArchive 读取并校验一个 skill tar：
//   - 所有条目路径必须在 tar 内部、不得越界（防解压逃逸）；
//   - 必须含一个 SKILL.md，且必须恰好位于唯一顶层目录内，即路径形如
//     `<技能名>/SKILL.md`（path.Dir 为单段、不含 '/'）；
//     根级 SKILL.md 或嵌套更深的 a/b/SKILL.md 均被拒绝；
//   - SKILL.md 必须有 YAML frontmatter 且含非空 name。
//
// 校验通过返回 SkillArchiveInfo；调用方负责另行限制 tar 大小。
func InspectSkillArchive(r io.Reader) (SkillArchiveInfo, error) {
	tr := tar.NewReader(r)
	// skillMD 保存已定位到的 SKILL.md 文件内容；
	// badLayoutPath 记录发现布局非法的 SKILL.md 路径（用于延迟报错）。
	var skillMD string
	var badLayoutPath string
	found := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return SkillArchiveInfo{}, fmt.Errorf("读取 skill tar 失败: %w", err)
		}
		clean := path.Clean(hdr.Name)
		// 路径安全校验：拒绝 .. 开头或绝对路径，防止解压逃逸。
		if strings.HasPrefix(clean, "..") || strings.HasPrefix(clean, "/") || strings.Contains(clean, "/../") {
			return SkillArchiveInfo{}, fmt.Errorf("%w: %s", ErrSkillArchiveUnsafePath, hdr.Name)
		}
		if hdr.Typeflag != tar.TypeReg || path.Base(clean) != "SKILL.md" {
			continue
		}
		// 找到了一个名为 SKILL.md 的文件，校验其在 tar 内的层级：
		// path.Dir 必须是单段目录名（不含 '/'），即 <技能名>/SKILL.md 结构。
		dir := path.Dir(clean)
		if dir == "." || strings.Contains(dir, "/") {
			// 根级（dir == "."）或深层嵌套（dir 含 '/'）均不合法；
			// 记录路径，遍历完成后统一报 ErrSkillArchiveBadLayout。
			badLayoutPath = clean
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return SkillArchiveInfo{}, fmt.Errorf("读取 SKILL.md 失败: %w", err)
		}
		skillMD = string(body)
		found = true
	}
	// 优先报布局错误：找到了 SKILL.md 但位置不合法。
	if badLayoutPath != "" && !found {
		return SkillArchiveInfo{}, fmt.Errorf("%w: %s", ErrSkillArchiveBadLayout, badLayoutPath)
	}
	if !found {
		return SkillArchiveInfo{}, ErrSkillArchiveNoSkillMD
	}
	name, err := parseSkillMDName(skillMD)
	if err != nil {
		return SkillArchiveInfo{}, err
	}
	return SkillArchiveInfo{Name: name}, nil
}

// parseSkillMDName 从 SKILL.md 的 YAML frontmatter 提取 name。
// frontmatter 约定以 "---" 行开头、再以 "---" 行结束。
func parseSkillMDName(body string) (string, error) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	if !strings.HasPrefix(body, "---\n") {
		return "", ErrSkillArchiveNoName
	}
	rest := body[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", ErrSkillArchiveNoName
	}
	var fm skillMDFrontmatter
	if err := yaml.Unmarshal([]byte(rest[:end]), &fm); err != nil {
		return "", fmt.Errorf("解析 SKILL.md frontmatter 失败: %w", err)
	}
	if strings.TrimSpace(fm.Name) == "" {
		return "", ErrSkillArchiveNoName
	}
	return strings.TrimSpace(fm.Name), nil
}
