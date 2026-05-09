package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

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

	if capturedID == "" {
		t.Errorf("RequestIDFromContext 应返回生成的 ID，got 空串")
	}
	if len(capturedID) != 32 {
		t.Errorf("生成的 ID 应为 32 字符 hex（16 字节），got %q (len=%d)", capturedID, len(capturedID))
	}
	resp := w.Header().Get(RequestIDHeader)
	if resp != capturedID {
		t.Errorf("response header X-Request-ID=%q 应与 ctx 中的一致 %q", resp, capturedID)
	}
}

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

	if capturedID != clientID {
		t.Errorf("应沿用客户端 ID %q，got %q", clientID, capturedID)
	}
	if w.Header().Get(RequestIDHeader) != clientID {
		t.Errorf("response header 应回写客户端 ID")
	}
}

func TestRequestIDFromContext_无值返回空串(t *testing.T) {
	got := RequestIDFromContext(httptest.NewRequest(http.MethodGet, "/x", nil).Context())
	if got != "" {
		t.Errorf("空 ctx 应返回空串，got %q", got)
	}
}

func TestGenerateRequestID_输出32字符hex(t *testing.T) {
	id := generateRequestID()
	if len(id) != 32 {
		t.Errorf("生成的 ID 应为 32 字符 hex，got %q (len=%d)", id, len(id))
	}
	for _, c := range id {
		ok := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !ok {
			t.Errorf("ID 含非 hex 字符 %q: %s", c, id)
			break
		}
	}
	// 简单防呆：连续两次生成不重复（极小概率失败可接受）
	if id2 := generateRequestID(); id == id2 {
		t.Errorf("两次生成应不同：%q vs %q", id, id2)
	}
	_ = strings.TrimSpace(id) // 占位避免 unused import
}
