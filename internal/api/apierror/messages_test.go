package apierror

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalizeReturnsLocaleMessage(t *testing.T) {
	// 用临时注册的 key 测解析：zh/en 各取对应；缺该语言回落 en；缺 key 回落 key 本身
	Register(map[MsgKey]map[string]string{
		"err.test.sample": {"zh": "测试", "en": "Test"},
	})
	assert.Equal(t, "测试", Localize("err.test.sample", "zh"))
	assert.Equal(t, "Test", Localize("err.test.sample", "en"))
	assert.Equal(t, "Test", Localize("err.test.sample", "fr"))
	assert.Equal(t, "err.test.missing", Localize("err.test.missing", "zh"))
}

func TestLocalizeFormatsArgs(t *testing.T) {
	// 带占位符的动态消息用 args 格式化
	Register(map[MsgKey]map[string]string{
		"err.test.fields": {"zh": "缺少必填参数: %s", "en": "Missing required parameters: %s"},
	})
	assert.Equal(t, "缺少必填参数: a, b", Localize("err.test.fields", "zh", "a, b"))
	assert.Equal(t, "Missing required parameters: a, b", Localize("err.test.fields", "en", "a, b"))
}

func TestCatalogEveryEntryHasBothLangs(t *testing.T) {
	// 守卫：catalog 每条都含 zh+en 且非空（随 domain 填充持续生效）
	for key, langs := range catalog {
		require.NotEmpty(t, langs["zh"], "key %s 缺 zh", key)
		require.NotEmpty(t, langs["en"], "key %s 缺 en", key)
	}
}

func TestJSONWritesLocalizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	Register(map[MsgKey]map[string]string{
		"err.test.json": {"zh": "无权", "en": "Forbidden"},
	})
	// en 上下文 → message 取 en；code 原样
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	SetLocale(c, "en")
	JSON(c, http.StatusForbidden, "FORBIDDEN", "err.test.json")
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.JSONEq(t, `{"code":"FORBIDDEN","message":"Forbidden"}`, w.Body.String())
}
