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

// TestFSSkillBlobStoreOpenSkillRejectsTraversal 验证 OpenSkill 拒绝目录穿越路径，
// 同时对不存在的合法路径返回打开失败错误——覆盖 traversal 拒绝与 open 失败两条分支。
func TestFSSkillBlobStoreOpenSkillRejectsTraversal(t *testing.T) {
	tmp := t.TempDir()
	bs := NewFSSkillBlobStore(tmp)

	// 穿越路径：../../etc/passwd 净化后落在 root 之外，应返回含"非法 skill 路径"的错误。
	_, err := bs.OpenSkill("../../etc/passwd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "非法 skill 路径")

	// 穿越路径变体：../sibling/x.tar 同样落在 root 之外，应被同等拒绝。
	_, err = bs.OpenSkill("../" + filepath.Base(tmp) + "_sibling/x.tar")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "非法 skill 路径")

	// 合法但不存在的路径：路径净化后仍在 root 内，应通过 traversal 检查并以打开失败结束。
	_, err = bs.OpenSkill("versions/v1/skills/missing.tar")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "非法 skill 路径", "合法路径不应触发 traversal 拒绝")
}
