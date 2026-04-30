// Package files 维护知识库主副本与工作目录的本地文件操作。
//
// SafePath 是所有写入/读取的入口，强制路径必须落在配置的根目录之内，
// 拒绝绝对路径、`..` 跳出、URL 编码绕过、符号链接和非常规文件。
package files

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// 与路径校验相关的错误。
var (
	ErrInvalidPath    = errors.New("非法的路径输入")
	ErrPathEscapesDir = errors.New("路径越出根目录")
	ErrPathSymlink    = errors.New("路径包含不允许的符号链接")
	ErrPathNotRegular = errors.New("路径不是普通文件或目录")
	ErrFileTooLarge   = errors.New("文件大小超过上限")
)

// SafeRoot 描述一个允许操作的根目录。
type SafeRoot struct {
	Root        string
	MaxFileSize int64
}

// NewSafeRoot 创建 SafeRoot；root 必须是绝对路径，否则返回错误。
func NewSafeRoot(root string, maxFileSize int64) (*SafeRoot, error) {
	if root == "" {
		return nil, fmt.Errorf("%w: 根目录不能为空", ErrInvalidPath)
	}
	cleaned, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if maxFileSize <= 0 {
		maxFileSize = 50 * 1024 * 1024
	}
	return &SafeRoot{Root: cleaned, MaxFileSize: maxFileSize}, nil
}

// Resolve 把相对路径转换为绝对路径，并校验它落在 root 之内。
// 不会创建任何文件；只做规范化和合法性判断。
func (r *SafeRoot) Resolve(relative string) (string, error) {
	if r == nil {
		return "", ErrInvalidPath
	}
	if strings.ContainsRune(relative, 0) {
		return "", fmt.Errorf("%w: 路径包含 NUL", ErrInvalidPath)
	}
	if filepath.IsAbs(relative) {
		return "", fmt.Errorf("%w: 不允许绝对路径", ErrInvalidPath)
	}
	decoded, err := url.PathUnescape(relative)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalidPath, err.Error())
	}
	cleaned := filepath.Clean(decoded)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("%w: 不允许的相对前缀", ErrInvalidPath)
	}
	if strings.Contains(cleaned, string(os.PathSeparator)+"..") {
		return "", fmt.Errorf("%w: 不允许跳出根目录", ErrPathEscapesDir)
	}
	resolved := filepath.Join(r.Root, cleaned)
	rel, err := filepath.Rel(r.Root, resolved)
	if err != nil {
		return "", fmt.Errorf("%w: 无法相对化 %s", ErrPathEscapesDir, err.Error())
	}
	if strings.HasPrefix(rel, "..") || rel == ".." {
		return "", ErrPathEscapesDir
	}
	if info, err := os.Lstat(resolved); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", ErrPathSymlink
		}
		if !info.Mode().IsDir() && !info.Mode().IsRegular() {
			return "", ErrPathNotRegular
		}
	}
	return resolved, nil
}

// EnsureSizeWithinLimit 在写入前检查内容长度。
func (r *SafeRoot) EnsureSizeWithinLimit(size int64) error {
	if r == nil {
		return ErrInvalidPath
	}
	if size > r.MaxFileSize {
		return fmt.Errorf("%w: %d > %d", ErrFileTooLarge, size, r.MaxFileSize)
	}
	return nil
}
