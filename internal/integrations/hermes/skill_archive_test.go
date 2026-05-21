package hermes

import (
	"archive/tar"
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeTar 构造一个内含指定文件的内存 tar，供测试使用。
func makeTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

// TestInspectSkillArchiveExtractsName 验证从 SKILL.md frontmatter 推导出 name。
func TestInspectSkillArchiveExtractsName(t *testing.T) {
	skillMD := "---\nname: weather-lookup\ndescription: 查天气\n---\n# 天气\n正文"
	data := makeTar(t, map[string]string{"weather/SKILL.md": skillMD})
	info, err := InspectSkillArchive(bytes.NewReader(data))
	require.NoError(t, err)
	assert.Equal(t, "weather-lookup", info.Name)
}

// TestInspectSkillArchiveRejectsMissingSkillMD 验证缺少 SKILL.md 时报错。
func TestInspectSkillArchiveRejectsMissingSkillMD(t *testing.T) {
	data := makeTar(t, map[string]string{"weather/readme.txt": "x"})
	_, err := InspectSkillArchive(bytes.NewReader(data))
	require.ErrorIs(t, err, ErrSkillArchiveNoSkillMD)
}

// TestInspectSkillArchiveRejectsNoName 验证 SKILL.md frontmatter 缺 name 时报错。
func TestInspectSkillArchiveRejectsNoName(t *testing.T) {
	data := makeTar(t, map[string]string{"SKILL.md": "---\ndescription: x\n---\n正文"})
	_, err := InspectSkillArchive(bytes.NewReader(data))
	require.ErrorIs(t, err, ErrSkillArchiveNoName)
}

// TestInspectSkillArchiveRejectsBadTar 验证非法 tar 字节报错。
func TestInspectSkillArchiveRejectsBadTar(t *testing.T) {
	_, err := InspectSkillArchive(bytes.NewReader([]byte("not a tar at all")))
	require.Error(t, err)
}

// TestInspectSkillArchiveRejectsUnsafePath 验证 tar 内含越界路径时报错。
func TestInspectSkillArchiveRejectsUnsafePath(t *testing.T) {
	data := makeTar(t, map[string]string{"../evil/SKILL.md": "---\nname: x\n---\n"})
	_, err := InspectSkillArchive(bytes.NewReader(data))
	require.ErrorIs(t, err, ErrSkillArchiveUnsafePath)
}
