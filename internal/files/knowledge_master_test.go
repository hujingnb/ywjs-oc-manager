package files

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKnowledgeMasterSaveAndList(t *testing.T) {
	master := newMaster(t, 1024)

	if err := master.Save("docs/intro.md", strings.NewReader("hello world"), 11); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	entries, err := master.List("docs")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "intro.md" || entries[0].Size != 11 {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestKnowledgeMasterRejectsOversize(t *testing.T) {
	master := newMaster(t, 4)

	err := master.Save("docs/big.md", strings.NewReader("hello world"), 11)
	if !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("error = %v, want ErrFileTooLarge", err)
	}
}

func TestKnowledgeMasterDeleteIsIdempotent(t *testing.T) {
	master := newMaster(t, 1024)
	if err := master.Save("doc.txt", strings.NewReader("hi"), 2); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := master.Delete("doc.txt"); err != nil {
		t.Fatalf("first Delete() error = %v", err)
	}
	if err := master.Delete("doc.txt"); err != nil {
		t.Fatalf("second Delete() error = %v", err)
	}
}

func TestKnowledgeMasterListRejectsEscapingPath(t *testing.T) {
	master := newMaster(t, 1024)
	if _, err := master.List("../../etc"); err == nil {
		t.Fatalf("expected error for escaping path")
	}
}

func TestKnowledgeMasterListReturnsSortedEntries(t *testing.T) {
	master := newMaster(t, 1024)
	if err := os.MkdirAll(filepath.Join(master.root.Root, "z-dir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := master.Save("a.md", strings.NewReader("a"), 1); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	entries, err := master.List(".")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 2 || !entries[0].IsDir || entries[1].Name != "a.md" {
		t.Fatalf("entries = %+v", entries)
	}
}

func newMaster(t *testing.T, max int64) *KnowledgeMaster {
	t.Helper()
	root, err := NewSafeRoot(t.TempDir(), max)
	if err != nil {
		t.Fatalf("NewSafeRoot() error = %v", err)
	}
	return NewKnowledgeMaster(root)
}
