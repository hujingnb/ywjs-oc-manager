// Package files 的 knowledge_master_test 覆盖知识库主副本的写入、列举和大小限制。
package files

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestKnowledgeMasterSaveAndList 验证知识库Master保存并列表的预期行为场景。
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

// TestKnowledgeMasterRejectsOversize 验证知识库Master拒绝超限的异常或拒绝路径场景。
func TestKnowledgeMasterRejectsOversize(t *testing.T) {
	master := newMaster(t, 4)

	err := master.Save("docs/big.md", strings.NewReader("hello world"), 11)
	require.ErrorIs(t, err, ErrFileTooLarge)
}

// TestKnowledgeMasterDeleteIsIdempotent 验证知识库Master删除保持幂等的特殊分支或幂等场景。
func TestKnowledgeMasterDeleteIsIdempotent(t *testing.T) {
	master := newMaster(t, 1024)
	err := master.Save("doc.txt", strings.NewReader("hi"), 2)
	require.NoError(t, err)
	err = master.Delete("doc.txt")
	require.NoError(t, err)
	err = master.Delete("doc.txt")
	require.NoError(t, err)
}

// TestKnowledgeMasterListRejectsEscapingPath 验证知识库Master列表拒绝越界路径的异常或拒绝路径场景。
func TestKnowledgeMasterListRejectsEscapingPath(t *testing.T) {
	master := newMaster(t, 1024)
	_, err := master.List("../../etc")
	require.Error(t, err)
}

// TestKnowledgeMasterListReturnsSortedEntries 验证知识库Master列表返回排序Entries的成功路径场景。
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
