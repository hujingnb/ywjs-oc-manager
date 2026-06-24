// client_conversation.go — ocops 会话端点的类型化客户端，转发 oc-ops /oc/conversations/*。
package ocops

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
)

// decodeListLenient 把 oc-ops 返回的会话/消息数组逐条解码到 T：先用 RawMessage 承接
// 顶层数组，再逐条 Unmarshal，单条解码失败只跳过该条并告警，不让整批失败。
//
// 动机：会话历史数据可能由不同 Hermes 版本写入（实例可在版本间切换，旧 /opt/data
// 数据保留），个别历史条目的字段类型可能与当前 struct 不一致。若沿用整批严格 Decode，
// 一条坏数据就会让整个列表端点返回 OUTPUT_INVALID、整页不可用；逐条容错可最大限度
// 展示可解析的数据，坏条目以 Warn 日志暴露便于排查。
func decodeListLenient[T any](ctx context.Context, raw []json.RawMessage, kind string) []T {
	out := make([]T, 0, len(raw))
	for _, item := range raw {
		var v T
		if err := json.Unmarshal(item, &v); err != nil {
			// raw 截断避免超长会话内容刷屏；含用户标题/预览，仅 Warn 级用于排障。
			snippet := string(item)
			if len(snippet) > 200 {
				snippet = snippet[:200]
			}
			slog.WarnContext(ctx, "跳过解码失败的会话条目", "kind", kind, "err", err, "raw", snippet)
			continue
		}
		out = append(out, v)
	}
	return out
}

// ListSessions 列出实例下会话；source 非空时按渠道过滤。
// GET /oc/conversations?source=&limit=&offset=
func (c *Client) ListSessions(ctx context.Context, ep Endpoint, source string, limit, offset int) ([]ConversationSession, error) {
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))
	if source != "" {
		q.Set("source", source)
	}
	// 顶层用 RawMessage 承接后逐条容错解码：单条会话字段异常不影响整列表。
	var raw []json.RawMessage
	if err := c.DoJSON(ctx, ep, http.MethodGet, "/oc/conversations?"+q.Encode(), nil, &raw); err != nil {
		return nil, err
	}
	return decodeListLenient[ConversationSession](ctx, raw, "session"), nil
}

// SessionMessages 读某会话历史消息。
// GET /oc/conversations/{sid}/messages
func (c *Client) SessionMessages(ctx context.Context, ep Endpoint, sid string) ([]ConversationMessage, error) {
	// 同 ListSessions：逐条容错，单条历史消息解码失败不影响整段历史展示。
	var raw []json.RawMessage
	if err := c.DoJSON(ctx, ep, http.MethodGet, "/oc/conversations/"+url.PathEscape(sid)+"/messages", nil, &raw); err != nil {
		return nil, err
	}
	return decodeListLenient[ConversationMessage](ctx, raw, "message"), nil
}

// CreateSession 新建会话（默认 web 来源），返回新建会话对象。
// POST /oc/conversations
func (c *Client) CreateSession(ctx context.Context, ep Endpoint, req ConversationCreateReq) (ConversationSession, error) {
	var out ConversationSession
	err := c.DoJSON(ctx, ep, http.MethodPost, "/oc/conversations", req, &out)
	return out, err
}

// DeleteSession 删除会话。
// DELETE /oc/conversations/{sid}
func (c *Client) DeleteSession(ctx context.Context, ep Endpoint, sid string) error {
	return c.DoJSON(ctx, ep, http.MethodDelete, "/oc/conversations/"+url.PathEscape(sid), nil, nil)
}

// SessionChat 续聊一轮，返回 assistant 回复。
// POST /oc/conversations/{sid}/chat
func (c *Client) SessionChat(ctx context.Context, ep Endpoint, sid string, req ConversationChatReq) (ConversationChatResult, error) {
	var out ConversationChatResult
	err := c.DoJSON(ctx, ep, http.MethodPost, "/oc/conversations/"+url.PathEscape(sid)+"/chat", req, &out)
	return out, err
}

// UpdateSessionTitle 重命名会话。
// PATCH /oc/conversations/{sid}
func (c *Client) UpdateSessionTitle(ctx context.Context, ep Endpoint, sid, title string) (ConversationSession, error) {
	var out ConversationSession
	err := c.DoJSON(ctx, ep, http.MethodPatch, "/oc/conversations/"+url.PathEscape(sid),
		map[string]string{"title": title}, &out)
	return out, err
}

// SessionChatStream 流式续聊，返回逐帧事件 channel；流结束/ctx 取消时关闭。
// POST /oc/conversations/{sid}/chat/stream
func (c *Client) SessionChatStream(ctx context.Context, ep Endpoint, sid string, req ConversationChatReq) (<-chan ConversationStreamEvent, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := c.openStreamBody(ctx, ep, http.MethodPost,
		"/oc/conversations/"+url.PathEscape(sid)+"/chat/stream", bytes.NewReader(b), "application/json")
	if err != nil {
		return nil, err
	}
	ch := make(chan ConversationStreamEvent, sseChanBuffer)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		scanSSE(ctx, resp.Body, func(data []byte) bool {
			var ev ConversationStreamEvent
			if err := json.Unmarshal(data, &ev); err != nil {
				return true // 跳过无法解析的帧
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
