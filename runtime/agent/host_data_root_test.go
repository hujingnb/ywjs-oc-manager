package main

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

// TestParseHostDataRootFromMountInfo_ExactMatch 验证解析宿主数据根目录来自挂载信息精确匹配的预期行为场景。
func TestParseHostDataRootFromMountInfo_ExactMatch(t *testing.T) {
	mountinfo := `27 32 0:24 / /sys rw shared:7 - sysfs sysfs rw
5010 4929 259:2 /home/hujing/dir/software/ywjs/oc-manager/.local/data/agent /var/lib/oc-agent rw,relatime - ext4 /dev/nvme0n1p2 rw
5011 4929 259:2 /etc/timezone /etc/timezone ro,relatime - ext4 /dev/nvme0n1p2 rw
`
	got, err := parseHostDataRootFromMountInfo(strings.NewReader(mountinfo), "/var/lib/oc-agent")
	require.NoError(t, err)
	want := "/home/hujing/dir/software/ywjs/oc-manager/.local/data/agent"
	assert.Equal(t, want, got)
}

// TestParseHostDataRootFromMountInfo_NotFoundReturnsEmpty 验证解析宿主数据根目录来自挂载信息未找到返回空值的异常或拒绝路径场景。
func TestParseHostDataRootFromMountInfo_NotFoundReturnsEmpty(t *testing.T) {
	mountinfo := `27 32 0:24 / /sys rw shared:7 - sysfs sysfs rw
32 2 259:2 / / rw,relatime shared:1 - ext4 /dev/nvme0n1p2 rw
`
	got, err := parseHostDataRootFromMountInfo(strings.NewReader(mountinfo), "/var/lib/oc-agent")
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

// TestParseHostDataRootFromMountInfo_OctalEscape 验证解析宿主数据根目录来自挂载信息八进制转义的预期行为场景。
func TestParseHostDataRootFromMountInfo_OctalEscape(t *testing.T) {
	// 路径含空格：space → \040
	mountinfo := `5010 4929 259:2 /home/with\040space/data /var/lib/oc-agent rw,relatime - ext4 /dev/nvme0n1p2 rw
`
	got, err := parseHostDataRootFromMountInfo(strings.NewReader(mountinfo), "/var/lib/oc-agent")
	require.NoError(t, err)
	want := "/home/with space/data"
	assert.Equal(t, want, got)
}

// TestUnescapeMountInfoField 验证反转义挂载信息字段的预期行为场景。
func TestUnescapeMountInfoField(t *testing.T) {
	cases := []struct{ in, want string }{
		{"abc", "abc"},                // 场景：普通字符串不需要反转义时保持原样
		{`a\040b`, "a b"},             // 场景：mountinfo 空格八进制转义应还原为空格
		{`a\011b`, "a\tb"},            // 场景：mountinfo 制表符八进制转义应还原为 tab
		{`a\134b`, `a\b`},             // 场景：mountinfo 反斜杠八进制转义应还原为反斜杠
		{`\040leading`, " leading"},   // 场景：开头位置的空格八进制转义应被还原
		{`trailing\040`, "trailing "}, // 场景：结尾位置的空格八进制转义应被还原
		{`\077`, "?"},                 // 场景：合法但项目内非常见的 octal 仍按通用规则还原为问号；0o077 = 63 = '?'
	}
	for _, c := range cases {
		assert.Equal(t, c.want, unescapeMountInfoField(c.in))
	}
}
