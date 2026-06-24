package i18n

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeLocale(t *testing.T) {
	// 受支持原样返回；区域后缀剥离；未知/空回落 def
	assert.Equal(t, "zh", NormalizeLocale("zh", "en"))
	assert.Equal(t, "zh", NormalizeLocale("zh-CN", "en"))
	assert.Equal(t, "en", NormalizeLocale("fr", "en"))
	assert.Equal(t, "en", NormalizeLocale("", "en"))
}

func TestParseAcceptLanguage(t *testing.T) {
	// 取首选标签并归一；多值取第一个；带 q 值忽略；缺失回落 def
	assert.Equal(t, "zh", ParseAcceptLanguage("zh-CN,zh;q=0.9,en;q=0.8", "en"))
	assert.Equal(t, "en", ParseAcceptLanguage("en-US,en;q=0.9", "zh"))
	assert.Equal(t, "zh", ParseAcceptLanguage("", "zh"))
}
