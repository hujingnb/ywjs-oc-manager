package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"oc-manager/internal/api/apierror"
)

func TestLocaleMiddlewareSetsLocale(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 场景：Accept-Language=zh-CN → 上下文 locale=zh
	r := gin.New()
	r.Use(Locale("en"))
	var got string
	r.GET("/x", func(c *gin.Context) { got = apierror.LocaleFrom(c); c.Status(200) })
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	r.ServeHTTP(httptest.NewRecorder(), req)
	assert.Equal(t, "zh", got)
}

func TestLocaleMiddlewareFallsBackToDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 场景：无 Accept-Language → 回落传入 default
	r := gin.New()
	r.Use(Locale("en"))
	var got string
	r.GET("/x", func(c *gin.Context) { got = apierror.LocaleFrom(c); c.Status(200) })
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	assert.Equal(t, "en", got)
}
