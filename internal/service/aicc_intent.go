package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/guregu/null/v5"

	"oc-manager/internal/store/sqlc"
)

const aiccIntentAnalyzerVersion = "aicc-lead-analysis/v1"

// aiccIntentAnalysis 是 aicc-lead-analysis Skill 的最小输出合同。证据必须来自访客原话，
// manager 会再次验证字段和值，模型不能借此写入任意画像或敏感信息。
type aiccIntentAnalysis struct {
	Level      string             `json:"level"`
	Fields     map[string]string  `json:"fields"`
	Confidence map[string]float64 `json:"confidence"`
	Evidence   map[string]string  `json:"evidence"`
}

var aiccIntentFieldAllowlist = map[string]struct{}{
	"budget": {}, "timeline": {}, "company": {}, "industry": {}, "role": {},
	"product_interest": {}, "team_size": {}, "contact": {},
}

// parseAICCIntentAnalysis 只接受白名单字段、合法等级及可在当前访客文本中找到的证据。
// 这能阻止 Skill 幻觉、跨轮记忆和手机号/身份证等敏感字段被悄悄沉淀。
func parseAICCIntentAnalysis(raw, visitorText string) (aiccIntentAnalysis, bool) {
	var analysis aiccIntentAnalysis
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &analysis) != nil {
		return aiccIntentAnalysis{}, false
	}
	if analysis.Level != "low" && analysis.Level != "medium" && analysis.Level != "high" {
		return aiccIntentAnalysis{}, false
	}
	clean := aiccIntentAnalysis{Level: analysis.Level, Fields: map[string]string{}, Confidence: map[string]float64{}, Evidence: map[string]string{}}
	for key, value := range analysis.Fields {
		if _, allowed := aiccIntentFieldAllowlist[key]; !allowed {
			continue
		}
		value = strings.TrimSpace(value)
		evidence := strings.TrimSpace(analysis.Evidence[key])
		if value == "" || evidence == "" || !strings.Contains(visitorText, evidence) {
			continue
		}
		// 明确不接收模型提取的敏感联系方式；正式联系方式只能由访客在表单里主动提交。
		if key == "contact" || looksLikeAICCSensitiveValue(value) {
			continue
		}
		clean.Fields[key] = value
		clean.Evidence[key] = evidence
		if confidence, exists := analysis.Confidence[key]; exists && confidence >= 0 && confidence <= 1 {
			clean.Confidence[key] = confidence
		}
	}
	return clean, true
}

func looksLikeAICCSensitiveValue(value string) bool {
	digits := 0
	for _, r := range value {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	return digits >= 8 || strings.Contains(value, "@")
}

// aiccIntentDecision 是 manager 在本轮回复前形成的唯一邀约决策；模型只能服从该决策，
// 不得自行把普通咨询升级为留资弹窗。
type aiccIntentDecision struct{ InviteStatus string }

// analyzeAICCIntent 在主回复前以独立 Hermes Turn 分析同一会话的访客原话。
// 返回 false 表示分析失败，调用方仍可交付普通答复，但必须将本任务保留为可重试状态。
func (d *AICCDispatcher) analyzeAICCIntent(ctx context.Context, task sqlc.AiccMessageTask, visitor sqlc.AiccMessage, conversation AICCConversationContext) (aiccIntentDecision, bool) {
	store, ok := d.store.(interface {
		GetAICCSessionIntent(context.Context, string) (sqlc.AiccSessionIntent, error)
		UpsertAICCSessionIntent(context.Context, sqlc.UpsertAICCSessionIntentParams) error
	})
	if !ok {
		return aiccIntentDecision{}, true
	}
	visitorText := aiccSessionVisitorText(conversation, visitor.TextContent.String)
	turn := AICCInboundTurn{
		TurnID:      task.MessageID + ":intent",
		SessionID:   task.SessionID,
		Channel:     "internal",
		Text:        visitorText,
		AppID:       task.AppID,
		Instruction: "使用 aicc-lead-analysis Skill 分析当前会话中以 visitor 标注的全部访客文本。仅输出 JSON：{\"level\":\"low|medium|high\",\"fields\":{},\"confidence\":{},\"evidence\":{}}。证据必须逐字来自当前会话访客文本；不得输出联系方式、身份证、地址或任何未在文本中的内容。",
	}
	reply, err := d.chat.ChatAICC(ctx, turn)
	if err != nil {
		return aiccIntentDecision{}, false
	}
	raw := reply.Raw
	if raw == "" {
		raw = reply.Text
	}
	analysis, valid := parseAICCIntentAnalysis(raw, visitorText)
	if !valid {
		return aiccIntentDecision{}, false
	}
	inviteStatus := "not_invited"
	previous, err := store.GetAICCSessionIntent(ctx, task.SessionID)
	if err == nil {
		inviteStatus = previous.InviteStatus
	} else if err != nil && err != sql.ErrNoRows {
		return aiccIntentDecision{}, false
	}
	inviteStatus = nextAICCInviteStatus(analysis.Level, inviteStatus)
	// 既有字段只能在本会话历史证据仍可回溯时保留；本轮同名字段以最新访客明确表达为准。
	mergeAICCIntentFields(&analysis, previous, visitorText)
	fields, _ := json.Marshal(analysis.Fields)
	confidence, _ := json.Marshal(analysis.Confidence)
	evidence, _ := json.Marshal(analysis.Evidence)
	if err := store.UpsertAICCSessionIntent(ctx, sqlc.UpsertAICCSessionIntentParams{
		ID: newUUID(), SessionID: task.SessionID, IntentLevel: analysis.Level,
		FieldsJson: fields, ConfidenceJson: confidence, EvidenceJson: evidence,
		AnalyzerVersion: aiccIntentAnalyzerVersion, AnalyzedMessageID: null.StringFrom(task.MessageID), InviteStatus: inviteStatus,
	}); err != nil {
		return aiccIntentDecision{}, false
	}
	return aiccIntentDecision{InviteStatus: inviteStatus}, true
}

// queueAICCIntentRetry 保存独立重试事实；主回复无需等待它成功。相同 session 的主键使重复失败
// 合并为一条记录，避免 worker 重启或消息重放制造无限重试队列。
func (d *AICCDispatcher) queueAICCIntentRetry(ctx context.Context, task sqlc.AiccMessageTask, reason string) {
	store, ok := d.store.(interface {
		UpsertAICCIntentAnalysisRetry(context.Context, sqlc.UpsertAICCIntentAnalysisRetryParams) error
	})
	if !ok {
		return
	}
	_ = store.UpsertAICCIntentAnalysisRetry(ctx, sqlc.UpsertAICCIntentAnalysisRetryParams{SessionID: task.SessionID, MessageID: task.MessageID, LastError: null.StringFrom(reason)})
}

// RetryPendingAICCIntentAnalysis 由后台循环调用。它不重放客服主回复，只对失败分析执行新 Hermes Turn，
// 成功后删除重试记录，因此任意次数重复扫描都不会生成第二条访客可见消息。
func (d *AICCDispatcher) RetryPendingAICCIntentAnalysis(ctx context.Context) error {
	store, ok := d.store.(interface {
		ListReadyAICCIntentAnalysisRetries(context.Context, int32) ([]sqlc.ListReadyAICCIntentAnalysisRetriesRow, error)
		DeleteAICCIntentAnalysisRetry(context.Context, sqlc.DeleteAICCIntentAnalysisRetryParams) error
		GetAICCMessageByID(context.Context, string) (sqlc.AiccMessage, error)
		GetAICCSessionContext(context.Context, string) (sqlc.AiccSessionContext, error)
		ListAICCContextMessages(context.Context, sqlc.ListAICCContextMessagesParams) ([]sqlc.AiccMessage, error)
	})
	if !ok {
		return nil
	}
	rows, err := store.ListReadyAICCIntentAnalysisRetries(ctx, 16)
	if err != nil {
		return err
	}
	for _, row := range rows {
		visitor, err := store.GetAICCMessageByID(ctx, row.MessageID)
		if err != nil {
			continue
		}
		contextData, err := BuildAICCConversationContext(ctx, store, row.SessionID, "")
		if err != nil {
			continue
		}
		task := sqlc.AiccMessageTask{MessageID: row.MessageID, SessionID: row.SessionID, AgentID: row.AgentID, OrgID: row.OrgID, AppID: row.AppID}
		if _, ready := d.analyzeAICCIntent(ctx, task, visitor, contextData); !ready {
			d.queueAICCIntentRetry(ctx, task, "intent analysis retry failed")
			continue
		}
		_ = store.DeleteAICCIntentAnalysisRetry(ctx, sqlc.DeleteAICCIntentAnalysisRetryParams{SessionID: row.SessionID, MessageID: row.MessageID})
	}
	return nil
}

// aiccSessionVisitorText 从 manager 重建的当前会话上下文中筛出访客消息；助手内容不能作为画像证据。
func aiccSessionVisitorText(conversation AICCConversationContext, current string) string {
	parts := make([]string, 0, len(conversation.Messages)+1)
	for _, message := range conversation.Messages {
		if message.Direction == "visitor" && strings.TrimSpace(message.Text) != "" {
			parts = append(parts, message.Text)
		}
	}
	parts = append(parts, current)
	return strings.Join(parts, "\n")
}

func mergeAICCIntentFields(analysis *aiccIntentAnalysis, previous sqlc.AiccSessionIntent, visitorText string) {
	if previous.ID == "" {
		return
	}
	var fields, evidence map[string]string
	if json.Unmarshal(previous.FieldsJson, &fields) != nil || json.Unmarshal(previous.EvidenceJson, &evidence) != nil {
		return
	}
	for key, value := range fields {
		if _, exists := analysis.Fields[key]; exists || strings.TrimSpace(evidence[key]) == "" || !strings.Contains(visitorText, evidence[key]) {
			continue
		}
		analysis.Fields[key], analysis.Evidence[key] = value, evidence[key]
	}
}

// constrainAICCIntentNextAction 是模型输出后的服务端最终防线。只有 manager 判定的首次高意向
// 才允许 offer_lead；其它任何状态一律剥离该动作，不能依赖提示词自觉。
func constrainAICCIntentNextAction(reply AICCResponseEnvelope, decision aiccIntentDecision) AICCResponseEnvelope {
	if decision.InviteStatus != "invited" || reply.NextAction == "offer_lead" && decision.InviteStatus != "invited" {
		if reply.NextAction == "offer_lead" {
			reply.NextAction = "none"
		}
		return reply
	}
	if reply.NextAction == "offer_lead" || decision.InviteStatus != "invited" {
		return reply
	}
	return reply
}

// nextAICCInviteStatus 只允许第一次高意向把 not_invited 推进到 invited；拒绝和提交都是访客决定，
// 后续模型分析绝不能覆盖它们。
func nextAICCInviteStatus(level, previous string) string {
	if level == "high" && previous == "not_invited" {
		return "invited"
	}
	return previous
}
