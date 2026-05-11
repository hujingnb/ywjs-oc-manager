// Package migrations 的测试只校验 embed.FS 内容，不连接真实数据库。
package migrations

import (
	"github.com/stretchr/testify/require"
	"io/fs"
	"strings"
	"testing"
)

func TestFS_ContainsUpAndDownPairs(t *testing.T) {
	entries, err := fs.ReadDir(FS, ".")
	require.NoError(t, err)
	// 每个 up 迁移都必须有同版本 down 文件，保证 cmd/migrate down 可回退一个版本。
	ups := make(map[string]struct{})
	downs := make(map[string]struct{})
	for _, e := range entries {
		switch {
		case strings.HasSuffix(e.Name(), ".up.sql"):
			ups[strings.TrimSuffix(e.Name(), ".up.sql")] = struct{}{}
		case strings.HasSuffix(e.Name(), ".down.sql"):
			downs[strings.TrimSuffix(e.Name(), ".down.sql")] = struct{}{}
		}
	}
	require.NotEqual(t, 0, len(ups))
	for version := range ups {
		if _, ok := downs[version]; !ok {
			t.Fatalf("迁移版本 %s 缺少 down 文件", version)
		}
	}
	for version := range downs {
		if _, ok := ups[version]; !ok {
			t.Fatalf("迁移版本 %s 缺少 up 文件", version)
		}
	}
}
