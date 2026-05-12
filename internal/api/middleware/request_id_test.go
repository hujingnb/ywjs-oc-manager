package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// TestRequestID_无header时生成新ID 验证请求IDheaderID的预期行为场景。
func TestRequestID_无header时生成新ID(t *testing.T) {
	r := gin.New()
	var capturedID string
	r.Use(RequestID())
	r.GET("/x", func(c *gin.Context) {
		capturedID = RequestIDFromContext(c.Request.Context())
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.NotEqual(t, "", capturedID)
	assert.Equal(t, 32, len(capturedID))
	resp := w.Header().Get(RequestIDHeader)
	assert.Equal(t, capturedID, resp)
}

// TestRequestID_有header时沿用客户端ID 验证请求IDheaderID的预期行为场景。
func TestRequestID_有header时沿用客户端ID(t *testing.T) {
	r := gin.New()
	var capturedID string
	r.Use(RequestID())
	r.GET("/x", func(c *gin.Context) {
		capturedID = RequestIDFromContext(c.Request.Context())
		c.Status(http.StatusOK)
	})

	const clientID = "client-trace-12345"
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(RequestIDHeader, clientID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, clientID, capturedID)
	assert.Equal(t, clientID, w.Header().Get(RequestIDHeader))
}

// TestRequestIDFromContext_无值返回空串 验证请求ID来自Context的预期行为场景。
func TestRequestIDFromContext_无值返回空串(t *testing.T) {
	got := RequestIDFromContext(httptest.NewRequest(http.MethodGet, "/x", nil).Context())
	assert.Equal(t, "", got)
}

// TestGenerateRequestID_输出32字符hex 验证Generate请求ID32hex的预期行为场景。
func TestGenerateRequestID_输出32字符hex(t *testing.T) {
	id := generateRequestID()
	assert.Equal(t, 32, len(id))
	for _, c := range id {
		ok := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !ok {
			t.Errorf("ID 含非 hex 字符 %q: %s", c, id)
			break
		}
	}
	// 简单防呆：连续两次生成不重复（极小概率失败可接受）
	id2 := generateRequestID()
	assert.NotEqual(t, id, id2)
	_ = strings.TrimSpace(id) // 占位避免 unused import
}
