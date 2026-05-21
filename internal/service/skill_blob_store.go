package service

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// FSSkillBlobStore 把 skill tar 存到 manager 本地数据根目录的
// versions/<versionID>/skills/<name>.tar，作为 manager 端主副本。
type FSSkillBlobStore struct {
	// root 是 manager 数据根目录（cfg.App.DataRoot）。
	root string
}

// NewFSSkillBlobStore 创建基于文件系统的 skill 主副本存储。
func NewFSSkillBlobStore(root string) *FSSkillBlobStore {
	return &FSSkillBlobStore{root: root}
}

// safeSegment 校验单个路径段不含分隔符 / .. 等危险字符。
func safeSegment(s string) error {
	if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "/\\") {
		return fmt.Errorf("非法路径段: %q", s)
	}
	return nil
}

// PutSkill 写入一个 skill tar，返回相对 root 的 '/' 分隔路径。
func (s *FSSkillBlobStore) PutSkill(versionID, skillName string, data []byte) (string, error) {
	if err := safeSegment(versionID); err != nil {
		return "", err
	}
	if err := safeSegment(skillName); err != nil {
		return "", err
	}
	rel := path.Join("versions", versionID, "skills", skillName+".tar")
	abs := filepath.Join(s.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("创建 skill 目录失败: %w", err)
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return "", fmt.Errorf("写入 skill tar 失败: %w", err)
	}
	return rel, nil
}

// DeleteSkill 删除一个 skill tar；文件不存在视为成功。
func (s *FSSkillBlobStore) DeleteSkill(relPath string) error {
	abs := filepath.Join(s.root, filepath.FromSlash(relPath))
	if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除 skill tar 失败: %w", err)
	}
	return nil
}

// OpenSkill 打开一个 skill tar 主副本用于读取；relPath 是 PutSkill 返回的相对 root 路径。
// 调用方负责 Close 返回的 ReadCloser。relPath 经路径净化后必须仍落在 root 内，防止 DB 中
// 异常 file_path 造成目录穿越读取。
func (s *FSSkillBlobStore) OpenSkill(relPath string) (io.ReadCloser, error) {
	abs := filepath.Join(s.root, filepath.FromSlash(relPath))
	// 净化后 abs 必须仍以 root 为前缀，否则视为非法路径。
	cleanRoot := filepath.Clean(s.root)
	if abs != cleanRoot && !strings.HasPrefix(abs, cleanRoot+string(filepath.Separator)) {
		return nil, fmt.Errorf("非法 skill 路径: %q", relPath)
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, fmt.Errorf("打开 skill tar 失败: %w", err)
	}
	return f, nil
}

// 编译时检查：FSSkillBlobStore 必须实现 SkillBlobStore 接口。
var _ SkillBlobStore = (*FSSkillBlobStore)(nil)
