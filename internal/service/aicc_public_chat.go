package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"oc-manager/internal/integrations/ocops"
)

// AICCPublicHermesChat 把 AICC 公开消息转发到绑定 hidden app 的 hermes runtime。
type AICCPublicHermesChat struct {
	ops      conversationOps
	resolver OcOpsResolver
}

// NewAICCPublicHermesChat 创建 AICC 公开消息转发器。
func NewAICCPublicHermesChat(ops conversationOps, resolver OcOpsResolver) *AICCPublicHermesChat {
	return &AICCPublicHermesChat{ops: ops, resolver: resolver}
}

// ChatAICC 转发单轮文字消息到 hidden app/hermes，并返回助手文本回复。
func (c *AICCPublicHermesChat) ChatAICC(ctx context.Context, appID, sessionID, text string) (string, error) {
	if c == nil || c.ops == nil || c.resolver == nil {
		return "", ErrConversationRuntimeUnavailable
	}
	loc, err := c.resolver.Resolve(ctx, appID)
	if err != nil {
		return "", err
	}
	if !loc.Supported || strings.TrimSpace(loc.Endpoint.BaseURL) == "" {
		return "", ErrConversationRuntimeUnavailable
	}
	hermesSessionID, err := c.ensureHermesSession(ctx, loc.Endpoint, sessionID)
	if err != nil {
		return "", err
	}
	out, err := c.ops.SessionChat(ctx, loc.Endpoint, hermesSessionID, ocops.ConversationChatReq{Message: text})
	if err != nil {
		return "", mapOcOpsConversationErr(err)
	}
	return conversationContentText(out.Message.Content), nil
}

func (c *AICCPublicHermesChat) ensureHermesSession(ctx context.Context, ep ocops.Endpoint, aiccSessionID string) (string, error) {
	title := "AICC " + aiccSessionID
	// Hermes 创建会话时接受 source=web，但持久化后可能按 api_server 回显；
	// 这里必须不带 source 过滤，只用 AICC 专属标题匹配，避免查不到已有会话后重复创建触发 400。
	sessions, err := c.ops.ListSessions(ctx, ep, "", 100, 0)
	if err != nil {
		return "", mapOcOpsConversationErr(err)
	}
	for _, session := range sessions {
		if session.Title == title && strings.TrimSpace(session.ID) != "" {
			return session.ID, nil
		}
	}
	created, err := c.ops.CreateSession(ctx, ep, ocops.ConversationCreateReq{
		Source: "web",
		Title:  title,
	})
	if err != nil {
		return "", mapOcOpsConversationErr(err)
	}
	if strings.TrimSpace(created.ID) == "" {
		return "", ErrConversationRuntimeUnavailable
	}
	return created.ID, nil
}

func conversationContentText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case json.RawMessage:
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			return s
		}
		return string(v)
	default:
		return fmt.Sprint(v)
	}
}
