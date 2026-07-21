package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	// aiccResponseMaxTextRunes 防止运行时输出异常长文本占满公开会话与数据库事务。
	aiccResponseMaxTextRunes = 4000
	// aiccResponseMaxSources 限制单条答复的可展示依据数量，避免模型伪造大量引用稀释审计。
	aiccResponseMaxSources = 8
)

// ErrAICCResponsePolicy 表示运行时回复虽可解析，但违反客服可展示、安全或来源可追溯规则。
var ErrAICCResponsePolicy = errors.New("aicc response policy violation")

// aiccEnterprisePriceClaimPattern 只识别带有具体金额或收费单位的价格承诺；访客提问
// “价格是多少”并不是模型作出的价格声明，不能因此误触发固定兜底。
var aiccEnterprisePriceClaimPattern = regexp.MustCompile(`(?i)(价格|报价|price).{0,24}(\d|元|人民币|rmb|usd|美元|每月|/month)|(\d|元|人民币|rmb|usd|美元|每月|/month).{0,24}(价格|报价|price)`)

// aiccOperationCompletedPatterns 覆盖常见的完成式、被动式和替代说法。匹配目标是
// “已完成某项外部操作”的断言，不能因为客服复述访客的操作诉求而误判。
var aiccOperationCompletedPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?:已|已经|成功)(?:帮|替)?(?:您|你)?.{0,12}(?:创建|新建|修改|更新|删除|执行|写入|部署|发布|启动|开通|重置)`),
	regexp.MustCompile(`(?:订单|网站|文件|服务|账号|密码).{0,12}(?:已|已经|成功).{0,6}(?:创建|新建|修改|更新|删除|执行|写入|部署|发布|启动|开通|重置|完成|上线)`),
	regexp.MustCompile(`(?:订单|网站|文件|服务|账号|密码).{0,12}(?:创建|新建|修改|更新|删除|执行|写入|部署|发布|启动|开通|重置).{0,6}(?:成功|完成|已完成|已经完成|上线)`),
	regexp.MustCompile(`(?:部署|发布).{0,6}(?:已|已经|成功).{0,6}(?:完成|上线)`),
}

// aiccEnterpriseNetworkDisclosurePattern 要求访客可见正文明确披露企业相关网页只是
// 未经企业确认的公开资料。仅在 source.unconfirmed 中标记不足以让访客理解信息边界。
var aiccEnterpriseNetworkDisclosurePattern = regexp.MustCompile(`公开(?:网络|资料|信息).{0,16}(?:未经|未获|尚未).{0,8}企业确认|(?:未经|未获|尚未).{0,8}企业确认.{0,16}公开(?:网络|资料|信息)`)

// AICCResponseToolAudit 是 manager 从当前轮受信任工具执行记录构造的引用白名单。
// key 是工具返回的稳定 reference_id，value 是该记录允许回显的来源事实。
type AICCResponseToolAudit map[string]AICCResponseSource

// aiccRawResponseEnvelope 是 Hermes 最终输出的严格 wire schema。flags 目前只承载
// refusal/fallback 两个展示状态，未知键一律拒绝，避免模型扩展为页面执行指令。
type aiccRawResponseEnvelope struct {
	Text       string               `json:"text"`
	Sources    []AICCResponseSource `json:"sources"`
	NextAction string               `json:"next_action"`
	Flags      json.RawMessage      `json:"flags"`
}

// ParseAndValidateAICCResponse 解析 Hermes 的最终 JSON，并把模型提供的来源与本轮工具审计逐项比对。
// 模型文本和 JSON 都不可信；只有通过本函数的结果才能写入公开会话。
func ParseAndValidateAICCResponse(raw string, audit AICCResponseToolAudit) (AICCResponseEnvelope, error) {
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(raw)))
	decoder.DisallowUnknownFields()
	var wire aiccRawResponseEnvelope
	if err := decoder.Decode(&wire); err != nil {
		return AICCResponseEnvelope{}, fmt.Errorf("%w: invalid JSON", ErrAICCResponsePolicy)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return AICCResponseEnvelope{}, fmt.Errorf("%w: trailing JSON", ErrAICCResponsePolicy)
	}
	if wire.Sources == nil || wire.Flags == nil {
		return AICCResponseEnvelope{}, fmt.Errorf("%w: sources and flags are required", ErrAICCResponsePolicy)
	}
	flags, err := normalizeAICCResponseFlags(wire.Flags)
	if err != nil {
		return AICCResponseEnvelope{}, err
	}
	for name := range flags {
		if name != "refusal" && name != "fallback" {
			return AICCResponseEnvelope{}, fmt.Errorf("%w: unknown flag", ErrAICCResponsePolicy)
		}
	}
	return validateAICCResponseEnvelope(AICCResponseEnvelope{Text: wire.Text, Sources: wire.Sources, NextAction: wire.NextAction, Refusal: flags["refusal"], Fallback: flags["fallback"]}, audit)
}

// normalizeAICCResponseFlags 兼容线上模型偶发把空 flags 对象输出成空数组的情况。
// 非空数组仍拒绝，避免模型借数组元素绕过固定的 refusal / fallback 布尔标记边界。
func normalizeAICCResponseFlags(raw json.RawMessage) (map[string]bool, error) {
	var flags map[string]bool
	if err := json.Unmarshal(raw, &flags); err == nil {
		if flags == nil {
			return nil, fmt.Errorf("%w: sources and flags are required", ErrAICCResponsePolicy)
		}
		return flags, nil
	}
	var emptyArray []json.RawMessage
	if err := json.Unmarshal(raw, &emptyArray); err == nil && len(emptyArray) == 0 {
		return map[string]bool{}, nil
	}
	return nil, fmt.Errorf("%w: invalid flags", ErrAICCResponsePolicy)
}

// validateAICCResponseEnvelope 校验已经由受信任适配层解码的响应。它也供 dispatcher 对重试结果
// 做最终防线；来源审计为空时不得携带任何来源。
func validateAICCResponseEnvelope(reply AICCResponseEnvelope, audit AICCResponseToolAudit) (AICCResponseEnvelope, error) {
	reply.Text = strings.TrimSpace(reply.Text)
	if reply.Text == "" || !utf8.ValidString(reply.Text) || utf8.RuneCountInString(reply.Text) > aiccResponseMaxTextRunes {
		return AICCResponseEnvelope{}, fmt.Errorf("%w: invalid text", ErrAICCResponsePolicy)
	}
	if reply.NextAction != "none" && reply.NextAction != "offer_lead" && reply.NextAction != "ask_resolution" {
		return AICCResponseEnvelope{}, fmt.Errorf("%w: invalid next action", ErrAICCResponsePolicy)
	}
	if len(reply.Sources) > aiccResponseMaxSources {
		return AICCResponseEnvelope{}, fmt.Errorf("%w: too many sources", ErrAICCResponsePolicy)
	}
	for i := range reply.Sources {
		if err := validateAICCResponseSource(&reply.Sources[i], audit); err != nil {
			return AICCResponseEnvelope{}, err
		}
	}
	if aiccResponseHasKnowledgeSource(reply.Sources) && aiccResponseHasEnterpriseNetworkSource(reply.Sources) {
		return AICCResponseEnvelope{}, fmt.Errorf("%w: enterprise knowledge takes precedence over enterprise network", ErrAICCResponsePolicy)
	}
	if aiccResponseHasEnterpriseNetworkSource(reply.Sources) && !aiccEnterpriseNetworkDisclosurePattern.MatchString(reply.Text) {
		return AICCResponseEnvelope{}, fmt.Errorf("%w: enterprise network source requires public-network unconfirmed disclosure", ErrAICCResponsePolicy)
	}
	if aiccResponseClaimsEnterprisePrice(reply.Text) && !aiccResponseHasKnowledgeSource(reply.Sources) {
		return AICCResponseEnvelope{}, fmt.Errorf("%w: enterprise price needs knowledge source", ErrAICCResponsePolicy)
	}
	if aiccResponseClaimsOperationCompleted(reply.Text) {
		return AICCResponseEnvelope{}, fmt.Errorf("%w: operational completion claim", ErrAICCResponsePolicy)
	}
	return reply, nil
}

// aiccResponseHasEnterpriseNetworkSource 识别未确认的企业相关网络资料。只要本轮已有企业知识，
// 就不允许同一答复引用该类资料，避免模型把冲突结论混入企业确认答案。
func aiccResponseHasEnterpriseNetworkSource(sources []AICCResponseSource) bool {
	for _, source := range sources {
		if source.Type == "web" && source.Scope == "enterprise_network" {
			return true
		}
	}
	return false
}

func validateAICCResponseSource(source *AICCResponseSource, audit AICCResponseToolAudit) error {
	if source == nil || (source.Type != "knowledge" && source.Type != "web") || strings.TrimSpace(source.ReferenceID) == "" {
		return fmt.Errorf("%w: invalid source", ErrAICCResponsePolicy)
	}
	trusted, ok := audit[source.ReferenceID]
	if !ok {
		return fmt.Errorf("%w: reference is absent from tool audit", ErrAICCResponsePolicy)
	}
	if trusted.ReferenceID != source.ReferenceID || source.Type != trusted.Type || source.URL != trusted.URL || source.Scope != trusted.Scope || source.Title != trusted.Title {
		return fmt.Errorf("%w: source differs from tool audit", ErrAICCResponsePolicy)
	}
	if source.URL != "" {
		u, err := url.Parse(source.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return fmt.Errorf("%w: invalid source URL", ErrAICCResponsePolicy)
		}
	}
	if source.Type == "web" && source.Scope != "public_network" && source.Scope != "enterprise_network" {
		return fmt.Errorf("%w: invalid web source scope", ErrAICCResponsePolicy)
	}
	// 企业相关的公开网络信息不是企业确认材料，模型和前端均不能把它包装成确定承诺。
	if source.Scope == "enterprise_network" && !source.Unconfirmed {
		return fmt.Errorf("%w: enterprise network source must be unconfirmed", ErrAICCResponsePolicy)
	}
	if source.Unconfirmed != trusted.Unconfirmed {
		return fmt.Errorf("%w: unconfirmed flag differs from tool audit", ErrAICCResponsePolicy)
	}
	return nil
}

func aiccResponseHasKnowledgeSource(sources []AICCResponseSource) bool {
	for _, source := range sources {
		if source.Type == "knowledge" {
			return true
		}
	}
	return false
}

func aiccResponseClaimsEnterprisePrice(text string) bool {
	return aiccEnterprisePriceClaimPattern.MatchString(text)
}

func aiccResponseClaimsOperationCompleted(text string) bool {
	for _, pattern := range aiccOperationCompletedPatterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	for _, phrase := range []string{
		"已为您创建", "已为你创建", "已为您修改", "已为你修改", "已为您删除", "已为你删除", "已为您执行", "已为你执行",
		"已为您写入", "已为你写入", "已为您部署", "已为你部署", "已为您启动", "已为你启动",
		"已经创建", "已经修改", "已经删除", "已经执行", "已经写入", "已经部署", "已经启动",
		"创建网站并启动服务", "已创建网站并启动服务", "文件写好了", "文件已经写好", "服务已启动", "网站已部署", "替你把服务跑起来",
		"已为您开通账号", "已为你开通账号", "账号已开通", "已开通账号",
		"已为您重置密码", "已为你重置密码", "密码已重置", "已经重置密码",
		"already created", "already updated", "already deleted", "already deployed", "service started", "account has been opened", "password has been reset",
	} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(phrase)) {
			return true
		}
	}
	return false
}
