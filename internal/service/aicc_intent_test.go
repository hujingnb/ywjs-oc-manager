package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestParseAICCIntentAnalysis 验证低中高意向只采纳当前访客原话可证明的白名单字段。
func TestParseAICCIntentAnalysis(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		visitor string
		level   string
		fields  map[string]string
		valid   bool
	}{
		// 低意向咨询不应被强行升级。
		{name: "低意向", raw: `{"level":"low","fields":{},"confidence":{},"evidence":{}}`, visitor: "你们是做什么的", level: "low", fields: map[string]string{}, valid: true},
		// 中意向字段必须能回溯到本轮访客原话。
		{name: "中意向有证据字段", raw: `{"level":"medium","fields":{"timeline":"下个月上线"},"confidence":{"timeline":0.8},"evidence":{"timeline":"下个月上线"}}`, visitor: "我们计划下个月上线", level: "medium", fields: map[string]string{"timeline": "下个月上线"}, valid: true},
		// 高意向仍只能保存受审核字段。
		{name: "高意向", raw: `{"level":"high","fields":{"budget":"预算 10 万"},"confidence":{"budget":0.9},"evidence":{"budget":"预算 10 万"}}`, visitor: "预算 10 万，尽快采购", level: "high", fields: map[string]string{"budget": "预算 10 万"}, valid: true},
		// 无访客证据的模型臆测必须丢弃字段而非写入画像。
		{name: "无证据字段", raw: `{"level":"medium","fields":{"company":"某公司"},"confidence":{},"evidence":{"company":"某公司"}}`, visitor: "想了解产品", level: "medium", fields: map[string]string{}, valid: true},
		// 联系方式属于敏感数据，即使模型在文本中找到也不能自动提取。
		{name: "敏感联系方式", raw: `{"level":"high","fields":{"contact":"13800138000"},"confidence":{},"evidence":{"contact":"13800138000"}}`, visitor: "我的电话是13800138000", level: "high", fields: map[string]string{}, valid: true},
		// 未知等级不能进入持久化。
		{name: "非法等级", raw: `{"level":"urgent","fields":{},"confidence":{},"evidence":{}}`, visitor: "立即购买", valid: false},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			result, ok := parseAICCIntentAnalysis(testCase.raw, testCase.visitor)
			assert.Equal(t, testCase.valid, ok)
			if !testCase.valid {
				return
			}
			assert.Equal(t, testCase.level, result.Level)
			assert.Equal(t, testCase.fields, result.Fields)
		})
	}
}

// TestNextAICCInviteStatus 验证首次高意向邀请和访客拒绝/提交后的不可逆边界。
func TestNextAICCInviteStatus(t *testing.T) {
	tests := []struct {
		name     string
		level    string
		previous string
		want     string
	}{
		// 第一次高意向才邀请留资。
		{name: "首次高意向", level: "high", previous: "not_invited", want: "invited"},
		// 中意向不应触发留资邀请。
		{name: "中意向", level: "medium", previous: "not_invited", want: "not_invited"},
		// 已拒绝的访客不再被模型重复邀请。
		{name: "已拒绝", level: "high", previous: "declined", want: "declined"},
		// 已提交联系方式后保留正式线索状态。
		{name: "已提交", level: "high", previous: "submitted", want: "submitted"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, nextAICCInviteStatus(testCase.level, testCase.previous))
		})
	}
}
