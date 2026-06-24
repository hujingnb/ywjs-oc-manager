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

// UpdateSessionTitle：PATCH /oc/conversations/{sid}，校验方法与路径，返回更新后的会话对象。
func TestUpdateSessionTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 校验请求方法为 PATCH
		assert.Equal(t, http.MethodPatch, r.Method)
		// 校验路径（sid 已 URL 编码）
		assert.Equal(t, "/oc/conversations/s1", r.URL.Path)
		// 返回更新后的会话对象
		_, _ = w.Write([]byte(`{"id":"s1","title":"新名"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client())
	out, err := c.UpdateSessionTitle(context.Background(), Endpoint{BaseURL: srv.URL, Token: "tk"}, "s1", "新名")
	require.NoError(t, err)
	// 断言解码后标题字段正确
	assert.Equal(t, "新名", out.Title)
}

// SessionChatStream：POST 携带 body，服务端返回两帧 SSE，channel 按序接收并正确解析。
func TestSessionChatStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 校验路径与方法
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/oc/conversations/s1/chat/stream", r.URL.Path)
		// 写两条 SSE 帧（命名事件格式，与 oc-ops 输出一致）
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"event\":\"assistant.delta\",\"payload\":{\"delta\":\"he\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"event\":\"assistant.completed\",\"payload\":{}}\n\n"))
	}))
	defer srv.Close()
	c := NewClient(srv.Client())
	ch, err := c.SessionChatStream(context.Background(), Endpoint{BaseURL: srv.URL, Token: "tk"},
		"s1", ConversationChatReq{Message: "hi"})
	require.NoError(t, err)
	// 从 channel 读出两条事件，校验事件名
	var events []ConversationStreamEvent
	for ev := range ch {
		events = append(events, ev)
	}
	require.Len(t, events, 2)
	assert.Equal(t, "assistant.delta", events[0].Event)     // 第一帧事件名
	assert.Equal(t, "assistant.completed", events[1].Event) // 第二帧事件名
}

// 加固回归：列表中单条会话字段类型异常（跨 Hermes 版本旧会话常见，如 started_at
// 本应是数字却为字符串）时，应跳过坏条、保留可解析的会话，而不是让整个会话列表
// 端点返回 OUTPUT_INVALID。复现线上「切版本后对话页整页报错」的根因场景。
func TestListSessionsLenientSkipsBadEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 中间一条 started_at 为字符串，与 ConversationSession.StartedAt(float64) 不匹配，
		// 严格整批解码会因这一条整体失败；逐条容错应只跳过它。
		_, _ = w.Write([]byte(`[{"id":"s1","source":"weixin"},{"id":"s2","started_at":"bad"},{"id":"s3","source":"web"}]`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client())
	out, err := c.ListSessions(context.Background(), Endpoint{BaseURL: srv.URL, Token: "tk"}, "", 50, 0)
	require.NoError(t, err)          // 坏条不再让整批失败
	require.Len(t, out, 2)           // 仅保留两条可解析会话
	assert.Equal(t, "s1", out[0].ID) // 第一条好数据保留
	assert.Equal(t, "s3", out[1].ID) // 坏条 s2 被跳过，s3 仍保留
}

// 加固边界：顶层不是 JSON 数组（如上游异常返回对象/错误信封却带 2xx）时，仍应
// 返回 ErrOutputInvalid，而非静默吞掉——逐条容错只放宽「单条坏」，不放宽「整体结构错」。
func TestListSessionsTopLevelNotArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 顶层是对象而非数组，承接的 []json.RawMessage 解码会失败
		_, _ = w.Write([]byte(`{"unexpected":"shape"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client())
	_, err := c.ListSessions(context.Background(), Endpoint{BaseURL: srv.URL, Token: "tk"}, "", 50, 0)
	require.ErrorIs(t, err, ErrOutputInvalid) // 整体结构错仍按 OUTPUT_INVALID 处理
}
