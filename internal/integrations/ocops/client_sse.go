// client_sse.go — ocops 包的 SSE（Server-Sent Events）流式客户端。
//
// oc-ops 的 kanban watch 与 channel login 接口返回 text/event-stream，
// 用一连串 `data: <json>` 帧逐条推送事件。本文件提供两个方法把这两条流
// 解析成强类型 channel：调用方读 channel 即可拿到逐条事件，流结束 / 出错 /
// ctx 取消时 channel 自动关闭，无需调用方手动 Close。
//
// 与 DoJSON 不同，SSE 是长连接：先同步发请求拿 resp（非 2xx 直接返回错误，
// 不建流），2xx 才起 goroutine 后台读流。goroutine 内统一 defer close(ch)
// + defer resp.Body.Close()，并 select ctx.Done() 以支持提前终止。
package ocops

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
)

// sseChanBuffer 是事件 channel 的缓冲大小。
// 给一个小 buffer 让读流 goroutine 在消费方短暂落后时不至于立即阻塞，
// 同时避免无界缓冲掩盖背压。
const sseChanBuffer = 8

// WatchKanban 订阅 board 的任务事件流：GET /oc/kanban/watch?board=<board>。
//
// 返回只读 channel，逐条投递 KanbanEvent；流正常结束、读到 `event: error`
// 帧、或 ctx 取消时关闭 channel。非 2xx 响应直接返回哨兵错误（不建流、
// channel 为 nil）。调用方应只读 channel 并依据 channel 关闭判断流结束，
// 不要持有 ctx 之外的取消手段。
func (c *Client) WatchKanban(ctx context.Context, ep Endpoint, board string) (<-chan KanbanEvent, error) {
	// kanban watch 走 query 传 board，与非流式 kanban 方法保持一致
	resp, err := c.openStream(ctx, ep, http.MethodGet, "/oc/kanban/watch?board="+board)
	if err != nil {
		return nil, err
	}

	ch := make(chan KanbanEvent, sseChanBuffer)
	go func() {
		// 统一在 goroutine 退出时关闭 channel 与响应体，保证不泄漏连接、
		// 消费方能感知流结束
		defer close(ch)
		defer resp.Body.Close()

		// data 帧回调：把一条 data JSON 解析为 KanbanEvent 并投递；
		// 解析失败的帧静默跳过（容忍服务端心跳 / 注释行等非事件帧）
		scanSSE(ctx, resp.Body, func(data []byte) bool {
			var ev KanbanEvent
			if err := json.Unmarshal(data, &ev); err != nil {
				return true // 跳过无法解析的帧，继续读流
			}
			// 投递时同样尊重 ctx 取消，避免消费方退出后 goroutine 卡在写 channel
			select {
			case ch <- ev:
				return true
			case <-ctx.Done():
				return false
			}
		})
	}()
	return ch, nil
}

// ChannelLogin 触发渠道登录并订阅其 SSE：POST /oc/channels/{channel}/login。
//
// 返回只读 channel，逐条投递 ChannelLoginEvent（qrcode/bound/timeout/failed）；
// 流结束、`event: error` 帧或 ctx 取消时关闭 channel。非 2xx 直接返回哨兵错误。
func (c *Client) ChannelLogin(ctx context.Context, ep Endpoint, channel string) (<-chan ChannelLoginEvent, error) {
	resp, err := c.openStream(ctx, ep, http.MethodPost, "/oc/channels/"+channel+"/login")
	if err != nil {
		return nil, err
	}

	ch := make(chan ChannelLoginEvent, sseChanBuffer)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanSSE(ctx, resp.Body, func(data []byte) bool {
			var ev ChannelLoginEvent
			if err := json.Unmarshal(data, &ev); err != nil {
				return true
			}
			select {
			case ch <- ev:
				return true
			case <-ctx.Done():
				return false
			}
		})
	}()
	return ch, nil
}

// ChannelLoginEvent 是 channel login SSE 的一条事件。
// Event ∈ {qrcode,bound,timeout,failed}：qrcode 用 URL 携带二维码链接，
// failed 用 Reason 携带失败原因；bound/timeout 仅有 Event。
// 字段对齐 server 端 channel_login 协程的 yield 结构。
type ChannelLoginEvent struct {
	// Event 是事件类型标识。
	Event string `json:"event"`
	// URL 是 qrcode 事件携带的二维码链接，其它事件为空。
	URL string `json:"url,omitempty"`
	// Reason 是 failed 事件携带的失败原因，其它事件为空。
	Reason string `json:"reason,omitempty"`
}

// openStream 发起一次无请求体的流式请求，是 openStreamBody 的薄包装。
// 保持现有 WatchKanban / ChannelLogin 调用方不变。
func (c *Client) openStream(ctx context.Context, ep Endpoint, method, path string) (*http.Response, error) {
	return c.openStreamBody(ctx, ep, method, path, nil, "")
}

// openStreamBody 发起一次流式请求并返回 resp（body 由调用方负责关闭）。
// 若 body 非 nil，会设置为请求体；若 contentType 非空，会设置 Content-Type 头。
// 非 2xx 时读取并关闭 body，按 statusToErr 映射哨兵错误返回（不建流）；
// 网络级错误归入 ErrCLI。请求会带上 Bearer 鉴权头与 Accept: text/event-stream。
func (c *Client) openStreamBody(ctx context.Context, ep Endpoint, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, ep.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+ep.Token)
	// 显式声明期望 SSE，便于服务端选择正确的响应内容类型
	req.Header.Set("Accept", "text/event-stream")
	// 仅当调用方提供了请求体时才设置 Content-Type，避免污染无 body 的 GET 请求
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	// 用无 Timeout 的 streamHTTP：普通 httpClient 的 Timeout 会中断流式 Body 读取，
	// 掐断 kanban watch / 微信扫码长连接；流的截止由 ctx 控制。
	resp, err := c.streamHTTP.Do(req)
	if err != nil {
		// 网络级错误与 DoJSON 保持一致归入 ErrCLI（上游不可达）
		return nil, ErrCLI
	}

	// 非 2xx：不建流，直接映射哨兵错误返回；先关闭 body 避免连接泄漏
	if sentinel := statusToErr(resp.StatusCode); sentinel != nil {
		resp.Body.Close()
		return nil, sentinel
	}
	return resp, nil
}

// scanSSE 按 SSE 帧逐行读取 r，对每条 data 帧调用 onData。
//
// 解析规则（SSE 标准的最小子集）：
//   - 以 "data:" 开头的行是数据行，去掉前缀及紧随的一个可选空格后即为帧数据；
//   - 以 "event: error" 标记的事件帧表示服务端报错，读到即结束（停止扫描）；
//   - 空行是帧分隔符（本实现按行投递，单帧单行 data，故空行仅作分隔忽略）；
//   - 其它行（注释 ":" 开头、id: 等）忽略。
//
// onData 返回 false 时停止扫描（用于 ctx 取消时的提前终止）。
// 每读到一行前先 select ctx.Done()，保证 ctx 取消能尽快跳出循环。
func scanSSE(ctx context.Context, r io.Reader, onData func(data []byte) bool) {
	scanner := bufio.NewScanner(r)
	// pendingError 标记当前帧是否为 event: error；读到帧分隔（空行）或下一帧时据此结束
	pendingError := false
	for scanner.Scan() {
		// ctx 取消优先：在处理每一行前检查，确保取消后迅速退出
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := scanner.Text()

		switch {
		case line == "":
			// 空行为帧分隔符；若上一帧标记了 error，分隔后即结束
			if pendingError {
				return
			}
		case len(line) >= 6 && line[:6] == "event:":
			// 识别 event: error 帧（容忍冒号后的前导空格）
			if v := trimSSEField(line[6:]); v == "error" {
				pendingError = true
			}
		case len(line) >= 5 && line[:5] == "data:":
			data := trimSSEField(line[5:])
			// error 帧的 data 仅用于记录，不投递为正常事件；读到即结束
			if pendingError {
				return
			}
			if !onData([]byte(data)) {
				return
			}
		default:
			// 忽略注释行（":" 开头）、id: / retry: 等其它字段
		}
	}
	// scanner 结束（EOF 或读错误）即流结束，正常返回让 caller 关闭 channel
}

// trimSSEField 去掉 SSE 字段值的一个可选前导空格。
// 按 SSE 规范，字段名冒号后若紧跟单个空格应被剥离（"data: x" → "x"），
// 但只剥离一个，保留数据本身可能含有的后续空格。
func trimSSEField(s string) string {
	if len(s) > 0 && s[0] == ' ' {
		return s[1:]
	}
	return s
}
