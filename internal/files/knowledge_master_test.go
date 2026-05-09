package files

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"github.com/stretchr/testify/require"
)

func TestKnowledgeMasterSaveAndList(t *testing.T) {
	master := newMaster(t, 1024)

	err := master.Save("docs/intro.md", strings.NewReader("hello world"), 11)
	require.NoError(t, err)
	entries, err := master.List("docs")
	require.NoError(t, err)
	if len(entries) != 1 || entries[0].Name != "intro.md" || entries[0].Size != 11 {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestKnowledgeMasterRejectsOversize(t *testing.T) {
	master := newMaster(t, 4)

	err := master.Save("docs/big.md", strings.NewReader("hello world"), 11)
	require.ErrorIs(t, err, ErrFileTooLarge)
}

func TestKnowledgeMasterDeleteIsIdempotent(t *testing.T) {
	master := newMaster(t, 1024)
	err := master.Save("doc.txt", strings.NewReader("hi"), 2)
	require.NoError(t, err)
	err = master.Delete("doc.txt")
	require.NoError(t, err)
	err = master.Delete("doc.txt")
	require.NoError(t, err)
}

func TestKnowledgeMasterListRejectsEscapingPath(t *testing.T) {
	master := newMaster(t, 1024)
	_, err := master.List("../../etc")
	require.Error(t, err)
}

func TestKnowledgeMasterListReturnsSortedEntries(t *testing.T) {
	master := newMaster(t, 1024)
	err := os.MkdirAll(filepath.Join(master.root.Root, "z-dir"), 0o755)
	require.NoError(t, err)
	err = master.Save("a.md", strings.NewReader("a"), 1)
	require.NoError(t, err)
	entries, err := master.List(".")
	require.NoError(t, err)
	if len(entries) != 2 || !entries[0].IsDir || entries[1].Name != "a.md" {
		t.Fatalf("entries = %+v", entries)
	}
}

func newMaster(t *testing.T, max int64) *KnowledgeMaster {
	t.Helper()
	root, err := NewSafeRoot(t.TempDir(), max)
	require.NoError(t, err)
	return NewKnowledgeMaster(root)
}
