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

// ChatAICC 为每个 Turn 创建独立 Hermes 临时会话。运行时容器可随时替换，
// 跨 Turn 的对话记忆完全由 manager 注入的 Context 承担，不能复用 Hermes 本地会话。
func (c *AICCPublicHermesChat) ChatAICC(ctx context.Context, turn AICCInboundTurn) (AICCResponseEnvelope, error) {
	if c == nil || c.ops == nil || c.resolver == nil {
		return AICCResponseEnvelope{}, ErrConversationRuntimeUnavailable
	}
	loc, err := c.resolver.Resolve(ctx, turn.AppID)
	if err != nil {
		return AICCResponseEnvelope{}, err
	}
	if !loc.Supported || strings.TrimSpace(loc.Endpoint.BaseURL) == "" {
		return AICCResponseEnvelope{}, ErrConversationRuntimeUnavailable
	}
	if strings.TrimSpace(turn.TurnID) == "" {
		return AICCResponseEnvelope{}, ErrConversationRuntimeUnavailable
	}
	created, err := c.ops.CreateSession(ctx, loc.Endpoint, ocops.ConversationCreateReq{Source: "web", Title: "AICC turn " + turn.TurnID})
	if err != nil {
		return AICCResponseEnvelope{}, mapAICCOpsConversationErr(err)
	}
	if strings.TrimSpace(created.ID) == "" {
		return AICCResponseEnvelope{}, ErrConversationRuntimeUnavailable
	}
	out, err := c.ops.SessionChat(ctx, loc.Endpoint, created.ID, ocops.ConversationChatReq{Message: renderAICCInboundTurn(turn)})
	if err != nil {
		return AICCResponseEnvelope{}, mapAICCOpsConversationErr(err)
	}
	// Hermes 有时把模型网关失败诊断包装进成功响应；这不是可展示的客服答复，
	// 必须转成可分类错误交给 dispatcher 写入 retry_wait / failed 状态。
	if err := aiccRuntimeDiagnosticError(conversationContentText(out.Message.Content)); err != nil {
		return AICCResponseEnvelope{}, err
	}
	// Raw 交由 dispatcher 统一解析并执行来源政策；Text 保留原文兼容当前调用方的运行时诊断与观测测试，
	// 但绝不能直接持久化为公开回复。
	content := conversationContentText(out.Message.Content)
	// 非流式 chat 完成响应只携带最终文本；本轮工具转录需从临时会话读取，才能让来源
	// reference_id 绑定实际执行过的受控工具，而不是由模型在最终 JSON 中自行声称。
	messages, err := c.ops.SessionMessages(ctx, loc.Endpoint, created.ID)
	if err != nil {
		return AICCResponseEnvelope{}, mapAICCOpsConversationErr(err)
	}
	return AICCResponseEnvelope{Text: content, Raw: content, ToolAudit: extractAICCResponseToolAudit(messages)}, nil
}

// aiccToolAuditPayload 是 Hermes AICC 受控工具在 tool transcript 中返回的最小审计载荷。
// 只有 aicc_response_sources 字段会被采纳，普通模型文本、tool_call 参数及未知工具一律无权生成来源。
type aiccToolAuditPayload struct {
	Sources []AICCResponseSource `json:"aicc_response_sources"`
}

// extractAICCResponseToolAudit 从当前临时会话的 tool transcript 中构造来源白名单。
// 每轮创建独立 Hermes 会话，因此此处天然不会混入历史轮次的工具结果。
func extractAICCResponseToolAudit(messages []ocops.ConversationMessage) AICCResponseToolAudit {
	audit := AICCResponseToolAudit{}
	for _, message := range messages {
		if message.Role != "tool" || !isAICCResponseAuditTool(message.ToolName) {
			continue
		}
		payload, ok := decodeAICCToolAuditPayload(message.Content)
		if !ok {
			continue
		}
		for _, source := range payload.Sources {
			if !isWellFormedAICCResponseAuditSource(source) || source.ReferenceID == "" {
				continue
			}
			// 同一 reference_id 的重复声明会使审计边界不确定，直接剔除，避免后出现的
			// 工具文本覆盖先前记录。
			if _, exists := audit[source.ReferenceID]; !exists {
				audit[source.ReferenceID] = source
			}
		}
	}
	return audit
}

// isAICCResponseAuditTool 是可输出来源审计的最小工具白名单；这与模型可见工具列表分离，
// 因为 skills_list/vision 等即使可调用也不能成为企业事实来源。
func isAICCResponseAuditTool(toolName string) bool {
	return toolName == "aicc_knowledge_search" || toolName == "web_search" || toolName == "web_extract"
}

// decodeAICCToolAuditPayload 将 oc-ops 解码后的任意 content 重新规范为 JSON；工具转录格式异常
// 仅意味着本轮没有可引用来源，绝不降级为信任模型提供的 sources。
func decodeAICCToolAuditPayload(content any) (aiccToolAuditPayload, bool) {
	var raw []byte
	switch value := content.(type) {
	case string:
		raw = []byte(value)
	case []byte:
		raw = value
	case json.RawMessage:
		raw = value
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return aiccToolAuditPayload{}, false
		}
		raw = encoded
	}
	var payload aiccToolAuditPayload
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Sources == nil {
		return aiccToolAuditPayload{}, false
	}
	return payload, true
}

// isWellFormedAICCResponseAuditSource 在进入白名单前校验受控工具回传的最小形态，
// 后续 response validator 还会将模型回显字段逐项与该白名单比较。
func isWellFormedAICCResponseAuditSource(source AICCResponseSource) bool {
	if (source.Type != "knowledge" && source.Type != "web") || strings.TrimSpace(source.Title) == "" {
		return false
	}
	if source.Type == "web" {
		if source.Scope != "public_network" && source.Scope != "enterprise_network" {
			return false
		}
		if source.URL == "" || !source.Unconfirmed && source.Scope == "enterprise_network" {
			return false
		}
	}
	return true
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

// renderAICCInboundTurn 将 manager 生成的历史置于数据边界，并把当前访客输入单独标注。
func renderAICCInboundTurn(turn AICCInboundTurn) string {
	return strings.Join([]string{strings.TrimSpace(turn.Instruction), RenderAICCConversationContext(turn.Context), "<current_visitor_message>", escapeAICCXML(strings.TrimSpace(turn.Text)), "</current_visitor_message>"}, "\n")
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
