package migrations

import (
	"io/fs"
	"strings"
	"testing"
)

func TestFS_ContainsUpAndDownPairs(t *testing.T) {
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		t.Fatalf("读取 embed FS 失败: %v", err)
	}
	var ups, downs int
	for _, e := range entries {
		switch {
		case strings.HasSuffix(e.Name(), ".up.sql"):
			ups++
		case strings.HasSuffix(e.Name(), ".down.sql"):
			downs++
		}
	}
	if ups == 0 {
		t.Fatal("embed FS 不含任何 *.up.sql，疑似 embed pattern 失效")
	}
	if ups != downs {
		t.Fatalf("up/down 数量不匹配: up=%d down=%d", ups, downs)
	}
}
