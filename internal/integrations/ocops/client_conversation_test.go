package ocops

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ListSessions：GET /oc/conversations 带 source query，解析为 []ConversationSession。
func TestListSessions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/oc/conversations", r.URL.Path)
		assert.Equal(t, "weixin", r.URL.Query().Get("source")) // source 透传
		assert.Equal(t, "Bearer tk", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`[{"id":"s1","source":"weixin","title":"张三"}]`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client())
	out, err := c.ListSessions(context.Background(), Endpoint{BaseURL: srv.URL, Token: "tk"}, "weixin", 50, 0)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "s1", out[0].ID)        // id 解析
	assert.Equal(t, "weixin", out[0].Source) // source 解析
}

// SessionChat：POST /oc/conversations/{sid}/chat 透传 message，解析回复。
func TestSessionChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/oc/conversations/s1/chat", r.URL.Path)
		_, _ = w.Write([]byte(`{"session_id":"s1","message":{"role":"assistant","content":"ok"}}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client())
	out, err := c.SessionChat(context.Background(), Endpoint{BaseURL: srv.URL, Token: "tk"},
		"s1", ConversationChatReq{Message: "hi"})
	require.NoError(t, err)
	assert.Equal(t, "ok", out.Message.Content) // assistant content 解析
}

// 404 → ErrNotFound（沿用 statusToErr 映射）。
func TestSessionMessagesNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"x"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client())
	_, err := c.SessionMessages(context.Background(), Endpoint{BaseURL: srv.URL, Token: "tk"}, "nope")
	require.ErrorIs(t, err, ErrNotFound)
}
