package service

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FS 实现的存取删往返：Put 后 Open 能读回原字节，Delete 后 Open 报错。
func TestFSLibraryBlobStore_RoundTrip(t *testing.T) {
	store := NewFSLibraryBlobStore(t.TempDir())
	data := []byte("hello-skill-tar")

	rel, err := store.PutLibrarySkill("platform", "weather", "1.0", "tar", data)
	require.NoError(t, err)
	assert.Equal(t, "library/platform/weather/1.0.tar", rel) // 相对路径以 / 分隔

	rc, err := store.OpenLibrarySkill(rel)
	require.NoError(t, err)
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	assert.Equal(t, data, got)

	require.NoError(t, store.DeleteLibrarySkill(rel))
	_, err = store.OpenLibrarySkill(rel)
	require.Error(t, err) // 删后不可读
}

// 非法路径段（含分隔符 / 上跳）被拒绝，防止写出根目录。
func TestFSLibraryBlobStore_RejectsUnsafeSegment(t *testing.T) {
	store := NewFSLibraryBlobStore(t.TempDir())
	_, err := store.PutLibrarySkill("platform", "../escape", "1.0", "tar", []byte("x"))
	require.Error(t, err)
}

// Open/Delete 传入越界 relPath（含 ..）被拒，防路径穿越。
func TestFSLibraryBlobStore_OpenDeleteRejectsTraversal(t *testing.T) {
	store := NewFSLibraryBlobStore(t.TempDir())
	_, err := store.OpenLibrarySkill("../../etc/passwd")
	require.Error(t, err)
	err = store.DeleteLibrarySkill("../../etc/passwd")
	require.Error(t, err)
}
