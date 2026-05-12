// Package files 的 safe_path_test 覆盖安全根目录对空路径、越界路径和合法相对路径的校验。
package files

import (
	"errors"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

// TestNewSafeRootRejectsEmpty 验证New安全根目录拒绝空值的异常或拒绝路径场景。
func TestNewSafeRootRejectsEmpty(t *testing.T) {
	_, err := NewSafeRoot("", 0)
	require.Error(t, err)
}

// TestSafeRootResolveRejectsAbsolute 验证安全根目录解析拒绝绝对的异常或拒绝路径场景。
func TestSafeRootResolveRejectsAbsolute(t *testing.T) {
	root := newTempRoot(t)
	if _, err := root.Resolve("/etc/passwd"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("error = %v, want ErrInvalidPath", err)
	}
}

// TestSafeRootResolveRejectsParentDir 验证安全根目录解析拒绝父级目录的异常或拒绝路径场景。
func TestSafeRootResolveRejectsParentDir(t *testing.T) {
	root := newTempRoot(t)
	_, err := root.Resolve("../escape")
	require.Error(t, err)
}

// TestSafeRootResolveRejectsURLEncodedTraversal 验证安全根目录解析拒绝URLEncoded路径穿越的异常或拒绝路径场景。
func TestSafeRootResolveRejectsURLEncodedTraversal(t *testing.T) {
	root := newTempRoot(t)
	_, err := root.Resolve("%2e%2e%2fescape")
	require.Error(t, err)
}

// TestSafeRootResolveAcceptsValidPath 验证安全根目录解析接受合法路径的预期行为场景。
func TestSafeRootResolveAcceptsValidPath(t *testing.T) {
	root := newTempRoot(t)
	got, err := root.Resolve("knowledge/file.txt")
	require.NoError(t, err)
	require.True(t, filepath.IsAbs(got))
}

// TestSafeRootResolveRejectsSymlink 验证安全根目录解析拒绝符号链接的异常或拒绝路径场景。
func TestSafeRootResolveRejectsSymlink(t *testing.T) {
	root := newTempRoot(t)
	target := filepath.Join(root.Root, "real.txt")
	err := os.WriteFile(target, []byte("hello"), 0o644)
	require.NoError(t, err)
	link := filepath.Join(root.Root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	if _, err := root.Resolve("link.txt"); !errors.Is(err, ErrPathSymlink) {
		t.Fatalf("error = %v, want ErrPathSymlink", err)
	}
}

// TestSafeRootResolveRejectsNUL 验证安全根目录解析拒绝NUL的异常或拒绝路径场景。
func TestSafeRootResolveRejectsNUL(t *testing.T) {
	root := newTempRoot(t)
	if _, err := root.Resolve("a\x00b"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("error = %v, want ErrInvalidPath", err)
	}
}

// TestSafeRootEnsureSizeWithinLimit 验证安全根目录确保Size使用inLimit的预期行为场景。
func TestSafeRootEnsureSizeWithinLimit(t *testing.T) {
	root := newTempRoot(t)
	if err := root.EnsureSizeWithinLimit(root.MaxFileSize + 1); !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("error = %v, want ErrFileTooLarge", err)
	}
	err := root.EnsureSizeWithinLimit(root.MaxFileSize)
	require.NoError(t, err)
}

func newTempRoot(t *testing.T) *SafeRoot {
	t.Helper()
	dir := t.TempDir()
	root, err := NewSafeRoot(dir, 1024)
	require.NoError(t, err)
	return root
}
