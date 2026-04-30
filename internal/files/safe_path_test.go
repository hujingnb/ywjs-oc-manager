package files

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNewSafeRootRejectsEmpty(t *testing.T) {
	if _, err := NewSafeRoot("", 0); err == nil {
		t.Fatalf("expected error for empty root")
	}
}

func TestSafeRootResolveRejectsAbsolute(t *testing.T) {
	root := newTempRoot(t)
	if _, err := root.Resolve("/etc/passwd"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("error = %v, want ErrInvalidPath", err)
	}
}

func TestSafeRootResolveRejectsParentDir(t *testing.T) {
	root := newTempRoot(t)
	if _, err := root.Resolve("../escape"); err == nil {
		t.Fatalf("expected error for parent dir traversal")
	}
}

func TestSafeRootResolveRejectsURLEncodedTraversal(t *testing.T) {
	root := newTempRoot(t)
	if _, err := root.Resolve("%2e%2e%2fescape"); err == nil {
		t.Fatalf("expected error for url encoded traversal")
	}
}

func TestSafeRootResolveAcceptsValidPath(t *testing.T) {
	root := newTempRoot(t)
	got, err := root.Resolve("knowledge/file.txt")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("expected absolute path, got %q", got)
	}
}

func TestSafeRootResolveRejectsSymlink(t *testing.T) {
	root := newTempRoot(t)
	target := filepath.Join(root.Root, "real.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
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
	if err := root.EnsureSizeWithinLimit(root.MaxFileSize); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func newTempRoot(t *testing.T) *SafeRoot {
	t.Helper()
	dir := t.TempDir()
	root, err := NewSafeRoot(dir, 1024)
	if err != nil {
		t.Fatalf("NewSafeRoot() error = %v", err)
	}
	return root
}
