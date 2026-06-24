package apierror_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// 扫描 internal/api 下非测试 .go：apierror.New 第二实参不应再是中文字面量(应走 msgKey)。
// 动态明细路径(safe/validation 经 redactlog/validationServiceMessage、运行时换算)传变量,天然不命中。
func TestNoHardcodedChineseInApiErrors(t *testing.T) {
	re := regexp.MustCompile(`apierror\.New\([^)]*[\x{4e00}-\x{9fff}]`)
	// 定位仓库根下的 internal/api（从本测试文件 internal/api/apierror 上溯两级）
	root := filepath.Join("..", "..", "..", "internal", "api")
	var offenders []string
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, _ := os.ReadFile(p)
		if re.Match(b) {
			offenders = append(offenders, p)
		}
		return nil
	})
	assert.Empty(t, offenders, "这些文件仍有裸中文 apierror.New，应改走 msgKey: %v", offenders)
}
