// client_conversation.go — ocops 会话端点的类型化客户端，转发 oc-ops /oc/conversations/*。
package ocops

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

// ListSessions 列出实例下会话；source 非空时按渠道过滤。
// GET /oc/conversations?source=&limit=&offset=
func (c *Client) ListSessions(ctx context.Context, ep Endpoint, source string, limit, offset int) ([]ConversationSession, error) {
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))
	if source != "" {
		q.Set("source", source)
	}
	var out []ConversationSession
	err := c.DoJSON(ctx, ep, http.MethodGet, "/oc/conversations?"+q.Encode(), nil, &out)
	return out, err
}

// SessionMessages 读某会话历史消息。
// GET /oc/conversations/{sid}/messages
func (c *Client) SessionMessages(ctx context.Context, ep Endpoint, sid string) ([]ConversationMessage, error) {
	var out []ConversationMessage
	err := c.DoJSON(ctx, ep, http.MethodGet, "/oc/conversations/"+url.PathEscape(sid)+"/messages", nil, &out)
	return out, err
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
