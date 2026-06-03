package service

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"oc-manager/internal/integrations/storage"
)

// LibraryBlobStore 是 skill 库内容（tar/zip 归档）的对象存储抽象。
// 平台库与已缓存的公共库 skill 都经它读写，key 走 library/ 共享前缀。
type LibraryBlobStore interface {
	// PutLibrarySkill 写入归档，返回以 / 分隔的相对 key。
	PutLibrarySkill(source, sourceRef, version, ext string, data []byte) (relPath string, err error)
	// DeleteLibrarySkill 按相对 key 删除归档。
	DeleteLibrarySkill(relPath string) error
	// OpenLibrarySkill 按相对 key 打开归档供读取。
	OpenLibrarySkill(relPath string) (io.ReadCloser, error)
}

// FSLibraryBlobStore 把归档落本地文件系统，供本地开发使用。
type FSLibraryBlobStore struct{ root string }

// NewFSLibraryBlobStore 以 root 为根目录构造 FS 实现。
func NewFSLibraryBlobStore(root string) *FSLibraryBlobStore { return &FSLibraryBlobStore{root: root} }

// librarySafeSegment 校验单个路径段合法：非空、非 . / ..、不含分隔符。
func librarySafeSegment(seg string) error {
	if seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, `/\`) {
		return fmt.Errorf("%w: 非法路径段 %q", ErrPlatformSkillInvalid, seg)
	}
	return nil
}

func (s *FSLibraryBlobStore) PutLibrarySkill(source, sourceRef, version, ext string, data []byte) (string, error) {
	for _, seg := range []string{source, sourceRef, version, ext} {
		if err := librarySafeSegment(seg); err != nil {
			return "", err
		}
	}
	rel := storage.LibrarySkillKey(source, sourceRef, version, ext)
	abs := filepath.Join(s.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("创建 skill 库目录失败: %w", err)
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return "", fmt.Errorf("写入 skill 归档失败: %w", err)
	}
	return rel, nil
}

// libraryAbsPath 把相对 key 解析为根目录内的绝对路径，拒绝越界（防路径穿越）。
func (s *FSLibraryBlobStore) libraryAbsPath(relPath string) (string, error) {
	abs := filepath.Join(s.root, filepath.FromSlash(relPath))
	cleanRoot := filepath.Clean(s.root)
	if abs != cleanRoot && !strings.HasPrefix(abs, cleanRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: 非法 skill 路径 %q", ErrPlatformSkillInvalid, relPath)
	}
	return abs, nil
}

func (s *FSLibraryBlobStore) DeleteLibrarySkill(relPath string) error {
	abs, err := s.libraryAbsPath(relPath)
	if err != nil {
		return err
	}
	if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除 skill 归档失败: %w", err)
	}
	return nil
}

func (s *FSLibraryBlobStore) OpenLibrarySkill(relPath string) (io.ReadCloser, error) {
	abs, err := s.libraryAbsPath(relPath)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, fmt.Errorf("打开 skill 归档失败: %w", err)
	}
	return f, nil
}

var _ LibraryBlobStore = (*FSLibraryBlobStore)(nil)
