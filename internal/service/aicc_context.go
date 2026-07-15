package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

const (
	aiccContextMessageLimit = 12
	aiccContextCharacterMax = 12000
)

// AICCConversationContextStore 只暴露构建当前会话上下文所需的只读查询。
type AICCConversationContextStore interface {
	GetAICCSessionContext(context.Context, string) (sqlc.AiccSessionContext, error)
	ListAICCContextMessages(context.Context, sqlc.ListAICCContextMessagesParams) ([]sqlc.AiccMessage, error)
}

// AICCContextMessage 是经过角色标注的原始对话记录，绝不提升为系统指令。
type AICCContextMessage struct {
	Direction string
	Text      string
}

// AICCConversationContext 是每个 Turn 从 manager 数据库重建的有界上下文。
type AICCConversationContext struct {
	Summary  string
	Messages []AICCContextMessage
}

// BuildAICCConversationContext 在当前 session 中读取摘要和稳定排序的消息窗口。
// 总字符超限时从最老原消息裁剪，保证新 Pod 也能以相同 DB 真相恢复最近对话。
func BuildAICCConversationContext(ctx context.Context, store AICCConversationContextStore, sessionID, excludeMessageID string) (AICCConversationContext, error) {
	if store == nil {
		return AICCConversationContext{}, fmt.Errorf("aicc conversation context store unavailable")
	}
	contextRow, err := store.GetAICCSessionContext(ctx, sessionID)
	if err != nil && err != sql.ErrNoRows {
		return AICCConversationContext{}, err
	}
	summary := ""
	if err == nil {
		summary = strings.TrimSpace(contextRow.Summary)
	}
	messages, err := store.ListAICCContextMessages(ctx, sqlc.ListAICCContextMessagesParams{SessionID: sessionID, ExcludeMessageID: excludeMessageID})
	if err != nil {
		return AICCConversationContext{}, err
	}
	items := make([]AICCContextMessage, 0, len(messages))
	for _, message := range messages {
		text := strings.TrimSpace(message.TextContent.String)
		if text == "" {
			continue
		}
		direction := "assistant"
		if message.Direction == domain.AICCMessageDirectionVisitor {
			direction = "visitor"
		}
		items = append(items, AICCContextMessage{Direction: direction, Text: text})
	}
	if len(items) > aiccContextMessageLimit {
		items = items[len(items)-aiccContextMessageLimit:]
	}
	// 摘要同样受总上下文预算约束；优先保留最新原消息，避免陈旧摘要挤掉当前问题。
	if utf8.RuneCountInString(summary) > aiccContextCharacterMax {
		summary = aiccLastRunes(summary, aiccContextCharacterMax)
	}
	used := utf8.RuneCountInString(summary)
	for len(items) > 0 && used+contextMessageCharacters(items) > aiccContextCharacterMax {
		items = items[1:]
	}
	return AICCConversationContext{Summary: summary, Messages: items}, nil
}

func contextMessageCharacters(messages []AICCContextMessage) int {
	total := 0
	for _, message := range messages {
		total += utf8.RuneCountInString(message.Text)
	}
	return total
}

// aiccLastRunes 以 rune 而非 byte 裁剪，避免中文和 emoji 在 UTF-8 中被截成非法字符串。
func aiccLastRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[len(runes)-max:])
}

// RenderAICCConversationContext 采用不可伪造的 XML 数据边界；访客历史只能作为数据，
// 即使其中出现“忽略指令”也不能成为系统或开发者指令。
func RenderAICCConversationContext(ctx AICCConversationContext) string {
	var builder strings.Builder
	builder.WriteString("<manager_conversation_context>\n")
	if ctx.Summary != "" {
		builder.WriteString("<summary>")
		builder.WriteString(escapeAICCXML(ctx.Summary))
		builder.WriteString("</summary>\n")
	}
	for _, message := range ctx.Messages {
		builder.WriteString("<message role=\"")
		builder.WriteString(message.Direction)
		builder.WriteString("\">")
		builder.WriteString(escapeAICCXML(message.Text))
		builder.WriteString("</message>\n")
	}
	builder.WriteString("</manager_conversation_context>")
	return builder.String()
}

func escapeAICCXML(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;")
	return replacer.Replace(value)
}
