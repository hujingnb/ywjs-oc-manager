package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFSSkillBlobStorePutAndDelete 验证写入后文件存在、删除后文件消失。
func TestFSSkillBlobStorePutAndDelete(t *testing.T) {
	root := t.TempDir()
	bs := NewFSSkillBlobStore(root)
	rel, err := bs.PutSkill("ver-1", "weather", []byte("tar-bytes"))
	require.NoError(t, err)
	assert.Equal(t, filepath.ToSlash("versions/ver-1/skills/weather.tar"), rel)
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	require.NoError(t, err)
	assert.Equal(t, "tar-bytes", string(content))
	require.NoError(t, bs.DeleteSkill(rel))
	_, err = os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	assert.True(t, os.IsNotExist(err))
}

// TestFSSkillBlobStoreRejectsUnsafeName 验证 skill 名含路径分隔符时被拒。
func TestFSSkillBlobStoreRejectsUnsafeName(t *testing.T) {
	bs := NewFSSkillBlobStore(t.TempDir())
	_, err := bs.PutSkill("ver-1", "../evil", []byte("x"))
	require.Error(t, err)
}
