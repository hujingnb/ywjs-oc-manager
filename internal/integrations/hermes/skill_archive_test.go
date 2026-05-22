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
// SKILL.md 放在合法的顶层目录内，确保触发的是 name 缺失而非布局错误。
func TestInspectSkillArchiveRejectsNoName(t *testing.T) {
	data := makeTar(t, map[string]string{"myskill/SKILL.md": "---\ndescription: x\n---\n正文"})
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

// TestInspectSkillArchiveRejectsRootLevelSkillMD 验证 SKILL.md 位于 tar 根目录时被拒。
// 容器端按 <技能名>/SKILL.md 结构解压，根级 SKILL.md 会污染解压目录。
func TestInspectSkillArchiveRejectsRootLevelSkillMD(t *testing.T) {
	// SKILL.md 直接位于根级，路径为 "SKILL.md"，应拒绝并返回 ErrSkillArchiveBadLayout。
	data := makeTar(t, map[string]string{"SKILL.md": "---\nname: badskill\n---\n正文"})
	_, err := InspectSkillArchive(bytes.NewReader(data))
	require.ErrorIs(t, err, ErrSkillArchiveBadLayout)
}

// TestInspectSkillArchiveRejectsTooDeepSkillMD 验证 SKILL.md 嵌套超过一级时被拒。
// 合法结构为 <技能名>/SKILL.md，a/b/SKILL.md 超出容器端预期层级。
func TestInspectSkillArchiveRejectsTooDeepSkillMD(t *testing.T) {
	// SKILL.md 嵌套在两级目录内，路径为 "a/b/SKILL.md"，应返回 ErrSkillArchiveBadLayout。
	data := makeTar(t, map[string]string{"a/b/SKILL.md": "---\nname: deepskill\n---\n正文"})
	_, err := InspectSkillArchive(bytes.NewReader(data))
	require.ErrorIs(t, err, ErrSkillArchiveBadLayout)
}

// TestInspectSkillArchiveAcceptsProperLayout 验证标准 <技能名>/SKILL.md 布局通过校验。
// 同时验证 SkillArchiveInfo.Name 来自 frontmatter，与目录名无关。
func TestInspectSkillArchiveAcceptsProperLayout(t *testing.T) {
	// verify-skill/SKILL.md，frontmatter name 与目录名一致，期望解析成功。
	skillMD := "---\nname: verify-skill\ndescription: 验证布局\n---\n# 正文\n内容"
	data := makeTar(t, map[string]string{"verify-skill/SKILL.md": skillMD})
	info, err := InspectSkillArchive(bytes.NewReader(data))
	require.NoError(t, err)
	assert.Equal(t, "verify-skill", info.Name)
}
