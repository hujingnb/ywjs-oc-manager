package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/api/handlers"
)

// TestConfigHandler_Public 覆盖公开配置端点：返回注入的默认语言与受支持语言集合，无需鉴权。
func TestConfigHandler_Public(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	handlers.RegisterPublicConfigRoutes(r, handlers.NewConfigHandler("en", []string{"en", "zh"}, false))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body handlers.PublicConfigResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "en", body.DefaultLocale)
	assert.Equal(t, []string{"en", "zh"}, body.SupportedLocales)
}
