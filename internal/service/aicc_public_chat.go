package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
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
		return "", mapAICCOpsConversationErr(err)
	}
	// Hermes 有时把模型网关失败诊断包装进成功响应；这不是可展示的客服答复，
	// 必须转成可分类错误交给 dispatcher 写入 retry_wait / failed 状态。
	if err := aiccRuntimeDiagnosticError(conversationContentText(out.Message.Content)); err != nil {
		return "", err
	}
	return conversationContentText(out.Message.Content), nil
}

var aiccRuntimeStatusPattern = regexp.MustCompile(`(?i)\b(429|503|529)\b`)

// aiccRuntimeRetryError 同时保留普通 Hermes 的错误语义和 AICC 专属的可重试原因。
// errors.Is 仍能匹配 ErrConversationCLI，dispatcher 则通过 errors.As 分类上游状态或超时。
type aiccRuntimeRetryError struct {
	public error
	cause  error
}

func (e *aiccRuntimeRetryError) Error() string   { return e.public.Error() }
func (e *aiccRuntimeRetryError) Unwrap() []error { return []error{e.public, e.cause} }

// mapAICCOpsConversationErr 仅供公开 AICC 转发使用；不能改变普通 Hermes handler 的 502 映射契约。
func mapAICCOpsConversationErr(err error) error {
	public := mapOcOpsConversationErr(err)
	var statusErr *ocops.HTTPStatusError
	if errors.As(err, &statusErr) && (statusErr.StatusCode == 429 || statusErr.StatusCode == 503 || statusErr.StatusCode == 529) {
		return &aiccRuntimeRetryError{public: public, cause: &AICCUpstreamStatusError{StatusCode: statusErr.StatusCode}}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &aiccRuntimeRetryError{public: public, cause: err}
	}
	var networkErr interface{ Timeout() bool }
	if errors.As(err, &networkErr) && networkErr.Timeout() {
		return &aiccRuntimeRetryError{public: public, cause: err}
	}
	return public
}

// aiccRuntimeDiagnosticError 识别 Hermes 错误文本中的上游状态；未知诊断保留确定性失败语义，
// 避免错误地把内部信息伪装成正常客服回复。
func aiccRuntimeDiagnosticError(reply string) error {
	lower := strings.ToLower(reply)
	if !strings.Contains(lower, "api call failed after") {
		return nil
	}
	if match := aiccRuntimeStatusPattern.FindStringSubmatch(reply); len(match) == 2 {
		var status int
		_, _ = fmt.Sscanf(match[1], "%d", &status)
		return &AICCUpstreamStatusError{StatusCode: status}
	}
	return fmt.Errorf("aicc runtime diagnostic: %s", reply)
}

func (c *AICCPublicHermesChat) ensureHermesSession(ctx context.Context, ep ocops.Endpoint, aiccSessionID string) (string, error) {
	title := "AICC " + aiccSessionID
	// Hermes 创建会话时接受 source=web，但持久化后可能按 api_server 回显；
	// 这里必须不带 source 过滤，只用 AICC 专属标题匹配，避免查不到已有会话后重复创建触发 400。
	sessions, err := c.ops.ListSessions(ctx, ep, "", 100, 0)
	if err != nil {
		return "", mapAICCOpsConversationErr(err)
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
		return "", mapAICCOpsConversationErr(err)
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
