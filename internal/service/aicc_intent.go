package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"

	"github.com/guregu/null/v5"

	"oc-manager/internal/store/sqlc"
)

const aiccIntentAnalyzerVersion = "aicc-lead-analysis/v1"

// aiccIntentAnalysis 是 aicc-lead-analysis Skill 的最小输出合同。证据必须来自访客原话，
// manager 会再次验证字段和值，模型不能借此写入任意画像或敏感信息。
type aiccIntentAnalysis struct {
	Level      string                        `json:"level"`
	Fields     map[string]string             `json:"fields"`
	Confidence map[string]float64            `json:"confidence"`
	Evidence   map[string]aiccIntentEvidence `json:"evidence"`
}

// aiccIntentEvidence 把每个字段绑定到具体访客消息，避免模型用“历史会话”这一模糊概念伪造依据。
type aiccIntentEvidence struct {
	MessageID string `json:"message_id"`
	Text      string `json:"text"`
}

var aiccIntentFieldAllowlist = map[string]struct{}{
	"budget": {}, "timeline": {}, "company": {}, "industry": {}, "role": {},
	"product_interest": {}, "team_size": {}, "contact": {},
}

// parseAICCIntentAnalysis 只接受白名单字段、合法等级及可在当前访客文本中找到的证据。
// 这能阻止 Skill 幻觉、跨轮记忆和手机号/身份证等敏感字段被悄悄沉淀。
func parseAICCIntentAnalysis(raw string, visitorMessages map[string]string) (aiccIntentAnalysis, bool) {
	var analysis aiccIntentAnalysis
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &analysis) != nil {
		return aiccIntentAnalysis{}, false
	}
	if analysis.Level != "low" && analysis.Level != "medium" && analysis.Level != "high" {
		return aiccIntentAnalysis{}, false
	}
	clean := aiccIntentAnalysis{Level: analysis.Level, Fields: map[string]string{}, Confidence: map[string]float64{}, Evidence: map[string]aiccIntentEvidence{}}
	for key, value := range analysis.Fields {
		if _, allowed := aiccIntentFieldAllowlist[key]; !allowed {
			continue
		}
		value = strings.TrimSpace(value)
		evidence := analysis.Evidence[key]
		evidence.Text = strings.TrimSpace(evidence.Text)
		messageText, belongs := visitorMessages[evidence.MessageID]
		if value == "" || evidence.Text == "" || !belongs || !strings.Contains(messageText, evidence.Text) {
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
type aiccIntentDecision struct {
	InviteStatus string
	AllowOffer   bool
}

// analyzeAICCIntent 在主回复前以独立 Hermes Turn 分析同一会话的访客原话。
// 返回 false 表示分析失败，调用方仍可交付普通答复，但必须将本任务保留为可重试状态。
func (d *AICCDispatcher) analyzeAICCIntent(ctx context.Context, task sqlc.AiccMessageTask, visitor sqlc.AiccMessage, conversation AICCConversationContext) (aiccIntentDecision, bool) {
	// 本地 E2E 注入器使用原子消费，确保并发 worker 至多制造一条失败重试事实，之后立即恢复真实分析路径。
	if d != nil && d.testFailIntentOnce.CompareAndSwap(true, false) {
		return aiccIntentDecision{}, false
	}
	store, ok := d.store.(interface {
		GetAICCSessionIntent(context.Context, string) (sqlc.AiccSessionIntent, error)
		UpsertAICCSessionIntent(context.Context, sqlc.UpsertAICCSessionIntentParams) error
	})
	if !ok {
		return aiccIntentDecision{}, true
	}
	visitorMessages := aiccSessionVisitorMessages(conversation, visitor)
	turn := AICCInboundTurn{
		TurnID:      task.MessageID + ":intent",
		SessionID:   task.SessionID,
		Channel:     "internal",
		Text:        renderAICCIntentEvidenceInput(visitorMessages),
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
	analysis, valid := parseAICCIntentAnalysis(raw, visitorMessages)
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
	allowOffer := analysis.Level == "high" && inviteStatus == "not_invited"
	// 首次高意向的展示许可在回复事务成功后才消费；此处保持 not_invited，
	// 以便主回复失败重试时仍可再次得到同一个首次邀约机会。
	if !allowOffer {
		inviteStatus = nextAICCInviteStatus(analysis.Level, inviteStatus)
	}
	// 既有字段只能在本会话历史证据仍可回溯时保留；本轮同名字段以最新访客明确表达为准。
	mergeAICCIntentFields(&analysis, previous, visitorMessages)
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
	return aiccIntentDecision{InviteStatus: inviteStatus, AllowOffer: allowOffer}, true
}

// renderAICCIntentEvidenceInput 将消息 ID 和原文一起置于受信任数据边界。Hermes 因此能输出可验证的
// evidence.message_id；XML 转义防止访客文本伪造分析协议标签。
func renderAICCIntentEvidenceInput(visitorMessages map[string]string) string {
	parts := make([]string, 0, len(visitorMessages))
	for id, text := range visitorMessages {
		parts = append(parts, `<visitor_message id="`+escapeAICCXML(id)+`">`+escapeAICCXML(text)+`</visitor_message>`)
	}
	sort.Strings(parts)
	return "<aicc_intent_evidence>\n" + strings.Join(parts, "\n") + "\n</aicc_intent_evidence>"
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
	// 本地 E2E 先验证失败记录已落库，再通过重启释放真实 worker；暂停期间不领取任务，避免竞态清理证据。
	if d != nil && d.testPauseIntentRetries.Load() {
		return nil
	}
	store, ok := d.store.(interface {
		ListReadyAICCIntentAnalysisRetries(context.Context, int32) ([]sqlc.ListReadyAICCIntentAnalysisRetriesRow, error)
		DeleteProcessedAICCIntentAnalysisRetry(context.Context, sqlc.DeleteProcessedAICCIntentAnalysisRetryParams) (int64, error)
		ClaimAICCIntentAnalysisRetry(context.Context, sqlc.ClaimAICCIntentAnalysisRetryParams) (int64, error)
		MarkAICCIntentAnalysisRetryProcessed(context.Context, sqlc.MarkAICCIntentAnalysisRetryProcessedParams) (int64, error)
		RescheduleClaimedAICCIntentAnalysisRetry(context.Context, sqlc.RescheduleClaimedAICCIntentAnalysisRetryParams) (int64, error)
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
		task := sqlc.AiccMessageTask{MessageID: row.MessageID, SessionID: row.SessionID, AgentID: row.AgentID, OrgID: row.OrgID, AppID: row.AppID}
		leaseToken := newUUID()
		claimed, err := store.ClaimAICCIntentAnalysisRetry(ctx, sqlc.ClaimAICCIntentAnalysisRetryParams{LeaseToken: null.StringFrom(leaseToken), SessionID: row.SessionID, MessageID: row.MessageID})
		if err != nil || claimed != 1 {
			continue
		}
		visitor, err := store.GetAICCMessageByID(ctx, row.MessageID)
		if err != nil {
			d.rescheduleClaimedAICCIntentRetry(ctx, task, leaseToken, "retry visitor message read failed")
			continue
		}
		contextData, err := BuildAICCConversationContext(ctx, store, row.SessionID, "")
		if err != nil {
			d.rescheduleClaimedAICCIntentRetry(ctx, task, leaseToken, "retry context build failed")
			continue
		}
		if _, ready := d.analyzeAICCIntent(ctx, task, visitor, contextData); !ready {
			d.rescheduleClaimedAICCIntentRetry(ctx, task, leaseToken, "intent analysis retry failed")
			continue
		}
		processed, err := store.MarkAICCIntentAnalysisRetryProcessed(ctx, sqlc.MarkAICCIntentAnalysisRetryProcessedParams{SessionID: row.SessionID, MessageID: row.MessageID, LeaseToken: null.StringFrom(leaseToken)})
		if err != nil || processed != 1 {
			d.rescheduleClaimedAICCIntentRetry(ctx, task, leaseToken, "retry completion mark failed")
			continue
		}
		// 清理失败仅留下已处理记录等待下次清扫，不能把它重新变成模型分析任务。
		_, _ = store.DeleteProcessedAICCIntentAnalysisRetry(ctx, sqlc.DeleteProcessedAICCIntentAnalysisRetryParams{SessionID: row.SessionID, MessageID: row.MessageID})
	}
	return nil
}

func (d *AICCDispatcher) rescheduleClaimedAICCIntentRetry(ctx context.Context, task sqlc.AiccMessageTask, leaseToken, reason string) {
	store, ok := d.store.(interface {
		RescheduleClaimedAICCIntentAnalysisRetry(context.Context, sqlc.RescheduleClaimedAICCIntentAnalysisRetryParams) (int64, error)
	})
	if ok {
		_, _ = store.RescheduleClaimedAICCIntentAnalysisRetry(ctx, sqlc.RescheduleClaimedAICCIntentAnalysisRetryParams{LastError: null.StringFrom(reason), SessionID: task.SessionID, MessageID: task.MessageID, LeaseToken: null.StringFrom(leaseToken)})
	}
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

func aiccSessionVisitorMessages(conversation AICCConversationContext, current sqlc.AiccMessage) map[string]string {
	items := map[string]string{current.ID: current.TextContent.String}
	for _, message := range conversation.Messages {
		if message.Direction == "visitor" && message.ID != "" {
			items[message.ID] = message.Text
		}
	}
	return items
}

func mergeAICCIntentFields(analysis *aiccIntentAnalysis, previous sqlc.AiccSessionIntent, visitorMessages map[string]string) {
	if previous.ID == "" {
		return
	}
	var fields map[string]string
	var evidence map[string]aiccIntentEvidence
	if json.Unmarshal(previous.FieldsJson, &fields) != nil || json.Unmarshal(previous.EvidenceJson, &evidence) != nil {
		return
	}
	for key, value := range fields {
		messageText, belongs := visitorMessages[evidence[key].MessageID]
		if _, exists := analysis.Fields[key]; exists || strings.TrimSpace(evidence[key].Text) == "" || !belongs || !strings.Contains(messageText, evidence[key].Text) {
			continue
		}
		analysis.Fields[key], analysis.Evidence[key] = value, evidence[key]
	}
}

// constrainAICCIntentNextAction 是模型输出后的服务端最终防线。只有 manager 判定的首次高意向
// 才允许 offer_lead；其它任何状态一律剥离该动作，不能依赖提示词自觉。
func constrainAICCIntentNextAction(reply AICCResponseEnvelope, decision aiccIntentDecision) AICCResponseEnvelope {
	if decision.AllowOffer {
		reply.NextAction = "offer_lead"
		return reply
	}
	if !decision.AllowOffer {
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
