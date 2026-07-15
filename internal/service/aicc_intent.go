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

// persistAICCIntent 在主回复完成后以独立低风险步骤运行。分析失败不能影响客服答复，
// 而同一 message_id 重试时会覆写同一会话画像，保持幂等。
func (d *AICCDispatcher) persistAICCIntent(ctx context.Context, task sqlc.AiccMessageTask, visitor sqlc.AiccMessage) {
	store, ok := d.store.(interface {
		GetAICCSessionIntent(context.Context, string) (sqlc.AiccSessionIntent, error)
		UpsertAICCSessionIntent(context.Context, sqlc.UpsertAICCSessionIntentParams) error
	})
	if !ok {
		return
	}
	turn := AICCInboundTurn{
		TurnID:      task.MessageID + ":intent",
		SessionID:   task.SessionID,
		Channel:     "internal",
		Text:        visitor.TextContent.String,
		AppID:       task.AppID,
		Instruction: "使用 aicc-lead-analysis Skill 分析当前访客文本。仅输出 JSON：{\"level\":\"low|medium|high\",\"fields\":{},\"confidence\":{},\"evidence\":{}}。证据必须逐字来自当前访客文本；不得输出联系方式、身份证、地址或任何未在文本中的内容。",
	}
	reply, err := d.chat.ChatAICC(ctx, turn)
	if err != nil {
		return
	}
	raw := reply.Raw
	if raw == "" {
		raw = reply.Text
	}
	analysis, valid := parseAICCIntentAnalysis(raw, visitor.TextContent.String)
	if !valid {
		return
	}
	inviteStatus := "not_invited"
	previous, err := store.GetAICCSessionIntent(ctx, task.SessionID)
	if err == nil {
		inviteStatus = previous.InviteStatus
	} else if err != nil && err != sql.ErrNoRows {
		return
	}
	inviteStatus = nextAICCInviteStatus(analysis.Level, inviteStatus)
	fields, _ := json.Marshal(analysis.Fields)
	confidence, _ := json.Marshal(analysis.Confidence)
	evidence, _ := json.Marshal(analysis.Evidence)
	_ = store.UpsertAICCSessionIntent(ctx, sqlc.UpsertAICCSessionIntentParams{
		ID: newUUID(), SessionID: task.SessionID, IntentLevel: analysis.Level,
		FieldsJson: fields, ConfidenceJson: confidence, EvidenceJson: evidence,
		AnalyzerVersion: aiccIntentAnalyzerVersion, AnalyzedMessageID: null.StringFrom(task.MessageID), InviteStatus: inviteStatus,
	})
}

// nextAICCInviteStatus 只允许第一次高意向把 not_invited 推进到 invited；拒绝和提交都是访客决定，
// 后续模型分析绝不能覆盖它们。
func nextAICCInviteStatus(level, previous string) string {
	if level == "high" && previous == "not_invited" {
		return "invited"
	}
	return previous
}
