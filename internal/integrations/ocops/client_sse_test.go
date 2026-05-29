// client_sse_test.go — SSE 流式客户端（WatchKanban / ChannelLogin）的 httptest 单测。
//
// 用 httptest.Server 返回 Content-Type: text/event-stream + 连续 `data: {...}\n\n`
// 帧，断言：多帧逐条投递且字段正确、流结束后 channel 关闭、ctx 取消能在合理时间内
// 关闭 channel。所有 goroutine 测试均以 ctx 超时兜底，避免泄漏 / 死锁。
package ocops_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/integrations/ocops"
)

// writeSSE 把一条 SSE data 帧写入 w 并立即 flush，模拟服务端逐帧推送。
func writeSSE(w http.ResponseWriter, flusher http.Flusher, data string) {
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

// TestWatchKanban 验证 WatchKanban 解析两个 KanbanEvent data 帧、逐条投递且字段正确，
// 服务端写完后 channel 关闭。
func TestWatchKanban(t *testing.T) {
	// 正常路径：服务端写两个 kanban 事件帧后结束流，断言收到 2 个事件后 channel 关闭
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 断言 method / path / query 与契约一致
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/oc/kanban/watch", r.URL.Path)
		assert.Equal(t, "b1", r.URL.Query().Get("board"))

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		// 两个事件帧：分别覆盖 created 与 status_changed，验证字段逐条解码
		writeSSE(w, flusher, `{"task_id":"t1","kind":"created","created_at":100}`)
		writeSSE(w, flusher, `{"task_id":"t2","kind":"status_changed","created_at":200}`)
	}))
	defer srv.Close()

	// 用带超时的 ctx 兜底，防止断言失败时 goroutine 永久阻塞
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	c, ep := newTestClient(srv)
	ch, err := c.WatchKanban(ctx, ep, "b1")
	require.NoError(t, err)

	// 第一个事件：created
	ev1 := recvEvent(t, ch)
	assert.Equal(t, "t1", ev1.TaskID)
	assert.Equal(t, "created", ev1.Kind)
	assert.Equal(t, int64(100), ev1.CreatedAt)

	// 第二个事件：status_changed
	ev2 := recvEvent(t, ch)
	assert.Equal(t, "t2", ev2.TaskID)
	assert.Equal(t, "status_changed", ev2.Kind)
	assert.Equal(t, int64(200), ev2.CreatedAt)

	// 流结束后 channel 应关闭
	requireClosed(t, ch)
}

// TestChannelLogin 验证 ChannelLogin 依次解析 qrcode→bound 两帧（method/path 正确），
// qrcode 携带 URL，随后 channel 关闭。
func TestChannelLogin(t *testing.T) {
	// 正常路径：服务端先推 qrcode（带 url）再推 bound，断言顺序与字段后 channel 关闭
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// channel login 走 POST，channel 在 path 段
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/oc/channels/wx-1/login", r.URL.Path)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		writeSSE(w, flusher, `{"event":"qrcode","url":"https://qr.example/abc"}`)
		writeSSE(w, flusher, `{"event":"bound"}`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	c, ep := newTestClient(srv)
	ch, err := c.ChannelLogin(ctx, ep, "wx-1")
	require.NoError(t, err)

	// 第一帧：qrcode，URL 正确
	ev1 := recvLoginEvent(t, ch)
	assert.Equal(t, "qrcode", ev1.Event)
	assert.Equal(t, "https://qr.example/abc", ev1.URL)

	// 第二帧：bound
	ev2 := recvLoginEvent(t, ch)
	assert.Equal(t, "bound", ev2.Event)
	assert.Empty(t, ev2.URL)

	requireClosed(t, ch)
}

// TestWatchKanbanContextCancel 验证 ctx 取消能在合理时间内关闭 channel：
// 服务端起一个持续不结束的流，cancel 后断言 channel 被关闭（不依赖服务端结束）。
func TestWatchKanbanContextCancel(t *testing.T) {
	// 边界路径：长连接永不主动结束，靠 ctx 取消驱动 goroutine 退出并关闭 channel
	srvClosed := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		// 先推一帧让客户端建立读流，然后阻塞直到请求 ctx 结束（客户端断开）
		writeSSE(w, flusher, `{"task_id":"t1","kind":"created","created_at":1}`)
		<-r.Context().Done()
		close(srvClosed)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c, ep := newTestClient(srv)
	ch, err := c.WatchKanban(ctx, ep, "b1")
	require.NoError(t, err)

	// 先收到首帧，确认流已建立
	ev := recvEvent(t, ch)
	assert.Equal(t, "t1", ev.TaskID)

	// 取消 ctx，channel 应在合理时间内关闭
	cancel()
	requireClosed(t, ch)

	// 服务端侧请求也应随之结束（连接断开），同样带超时兜底
	select {
	case <-srvClosed:
	case <-time.After(3 * time.Second):
		// 不强制失败：部分实现下服务端感知断开有延迟，核心断言是客户端 channel 已关闭
	}
}

// recvEvent 在超时兜底下从 KanbanEvent channel 取一个事件；超时或提前关闭即 fail。
func recvEvent(t *testing.T, ch <-chan ocops.KanbanEvent) ocops.KanbanEvent {
	t.Helper()
	select {
	case ev, ok := <-ch:
		require.True(t, ok, "channel 在收到预期事件前被关闭")
		return ev
	case <-time.After(2 * time.Second):
		require.FailNow(t, "等待事件超时")
		return ocops.KanbanEvent{}
	}
}

// recvLoginEvent 在超时兜底下从 ChannelLoginEvent channel 取一个事件；超时或提前关闭即 fail。
func recvLoginEvent(t *testing.T, ch <-chan ocops.ChannelLoginEvent) ocops.ChannelLoginEvent {
	t.Helper()
	select {
	case ev, ok := <-ch:
		require.True(t, ok, "channel 在收到预期事件前被关闭")
		return ev
	case <-time.After(2 * time.Second):
		require.FailNow(t, "等待登录事件超时")
		return ocops.ChannelLoginEvent{}
	}
}

// requireClosed 在超时兜底下断言 channel 已关闭（读到零值且 ok=false）。
// 泛型适配两种事件 channel，避免重复 helper。
func requireClosed[T any](t *testing.T, ch <-chan T) {
	t.Helper()
	select {
	case _, ok := <-ch:
		require.False(t, ok, "channel 应已关闭，却仍能读到事件")
	case <-time.After(2 * time.Second):
		require.FailNow(t, "等待 channel 关闭超时")
	}
}
