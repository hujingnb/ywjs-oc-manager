package main

import (
	"strings"
	"testing"
)

func TestParseHostDataRootFromMountInfo_ExactMatch(t *testing.T) {
	mountinfo := `27 32 0:24 / /sys rw shared:7 - sysfs sysfs rw
5010 4929 259:2 /home/hujing/dir/software/ywjs/oc-manager/.local/data/agent /var/lib/oc-agent rw,relatime - ext4 /dev/nvme0n1p2 rw
5011 4929 259:2 /etc/timezone /etc/timezone ro,relatime - ext4 /dev/nvme0n1p2 rw
`
	got, err := parseHostDataRootFromMountInfo(strings.NewReader(mountinfo), "/var/lib/oc-agent")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	want := "/home/hujing/dir/software/ywjs/oc-manager/.local/data/agent"
	if got != want {
		t.Errorf("got=%q want=%q", got, want)
	}
}

func TestParseHostDataRootFromMountInfo_NotFoundReturnsEmpty(t *testing.T) {
	mountinfo := `27 32 0:24 / /sys rw shared:7 - sysfs sysfs rw
32 2 259:2 / / rw,relatime shared:1 - ext4 /dev/nvme0n1p2 rw
`
	got, err := parseHostDataRootFromMountInfo(strings.NewReader(mountinfo), "/var/lib/oc-agent")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if got != "" {
		t.Errorf("期望未匹配返回空，实际 %q", got)
	}
}

func TestParseHostDataRootFromMountInfo_OctalEscape(t *testing.T) {
	// 路径含空格：space → \040
	mountinfo := `5010 4929 259:2 /home/with\040space/data /var/lib/oc-agent rw,relatime - ext4 /dev/nvme0n1p2 rw
`
	got, err := parseHostDataRootFromMountInfo(strings.NewReader(mountinfo), "/var/lib/oc-agent")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	want := "/home/with space/data"
	if got != want {
		t.Errorf("got=%q want=%q", got, want)
	}
}

func TestUnescapeMountInfoField(t *testing.T) {
	cases := []struct{ in, want string }{
		{"abc", "abc"},
		{`a\040b`, "a b"},
		{`a\011b`, "a\tb"},
		{`a\134b`, `a\b`},
		{`\040leading`, " leading"},
		{`trailing\040`, "trailing "},
		{`\077`, "?"}, // 0o077 = 63 = '?' (合法 octal 但项目内不会出现)
	}
	for _, c := range cases {
		if got := unescapeMountInfoField(c.in); got != c.want {
			t.Errorf("unescape %q got=%q want=%q", c.in, got, c.want)
		}
	}
}
