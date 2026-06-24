// client_config_test.go — Config 方法（GET /oc/config）的 httptest 单测。
//
// 验证 Config 正确发出 GET /oc/config 并把响应中 display_language 字段解码为
// OcConfig.DisplayLanguage。
package ocops_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/integrations/ocops"
)

// TestClientConfigParsesDisplayLanguage 验证 Config 发出 GET /oc/config
// 并把响应中 display_language 字段解码为 OcConfig.DisplayLanguage。
func TestClientConfigParsesDisplayLanguage(t *testing.T) {
	// 正常路径：server 返回 display_language=zh，断言字段解码正确，method/path 与契约一致
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/oc/config", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"display_language":"zh"}`))
	}))
	defer srv.Close()

	c := ocops.NewClient(http.DefaultClient)
	ep := ocops.Endpoint{BaseURL: srv.URL, Token: "test-token"}
	out, err := c.Config(context.Background(), ep)
	require.NoError(t, err)
	assert.Equal(t, "zh", out.DisplayLanguage)
}
