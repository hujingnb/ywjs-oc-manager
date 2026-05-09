package files

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"github.com/stretchr/testify/require"
)

func TestNewSafeRootRejectsEmpty(t *testing.T) {
	_, err := NewSafeRoot("", 0)
	require.Error(t, err)
}

func TestSafeRootResolveRejectsAbsolute(t *testing.T) {
	root := newTempRoot(t)
	if _, err := root.Resolve("/etc/passwd"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("error = %v, want ErrInvalidPath", err)
	}
}

func TestSafeRootResolveRejectsParentDir(t *testing.T) {
	root := newTempRoot(t)
	_, err := root.Resolve("../escape")
	require.Error(t, err)
}

func TestSafeRootResolveRejectsURLEncodedTraversal(t *testing.T) {
	root := newTempRoot(t)
	_, err := root.Resolve("%2e%2e%2fescape")
	require.Error(t, err)
}

func TestSafeRootResolveAcceptsValidPath(t *testing.T) {
	root := newTempRoot(t)
	got, err := root.Resolve("knowledge/file.txt")
	require.NoError(t, err)
	require.True(t, filepath.IsAbs(got))
}

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

func TestSafeRootResolveRejectsNUL(t *testing.T) {
	root := newTempRoot(t)
	if _, err := root.Resolve("a\x00b"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("error = %v, want ErrInvalidPath", err)
	}
}

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
