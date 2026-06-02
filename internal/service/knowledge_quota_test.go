package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNormalizeKnowledgeQuotaBytes 验证知识库容量默认值与正数校验。
func TestNormalizeKnowledgeQuotaBytes(t *testing.T) {
	oneGB := KnowledgeQuotaDefaultBytes

	got, err := normalizeKnowledgeQuotaBytes(nil)
	require.NoError(t, err)
	assert.Equal(t, oneGB, got)

	custom := int64(2 * 1024 * 1024 * 1024)
	got, err = normalizeKnowledgeQuotaBytes(&custom)
	require.NoError(t, err)
	assert.Equal(t, custom, got)

	zero := int64(0)
	_, err = normalizeKnowledgeQuotaBytes(&zero)
	require.ErrorIs(t, err, ErrMemberCreateInvalid)
}

// TestValidateKnowledgeQuotaBytes 验证显式提交的知识库容量必须为正数。
func TestValidateKnowledgeQuotaBytes(t *testing.T) {
	cases := []struct {
		name    string
		value   int64
		wantErr bool
	}{
		{"正数容量通过校验", KnowledgeQuotaDefaultBytes, false}, // 场景：更新路径提交正数容量时允许保存。
		{"零容量返回成员资料非法错误", 0, true},                      // 场景：更新路径提交 0 时拒绝并保留统一入参错误类型。
		{"负数容量返回成员资料非法错误", -1, true},                    // 场景：更新路径提交负数时拒绝并保留统一入参错误类型。
	}

	for _, c := range cases {
		// 当前子测试覆盖表格用例中该名称对应的显式容量输入和错误期望。
		t.Run(c.name, func(t *testing.T) {
			err := validateKnowledgeQuotaBytes(c.value)
			if c.wantErr {
				require.ErrorIs(t, err, ErrMemberCreateInvalid)
				return
			}
			require.NoError(t, err)
		})
	}
}

// TestKnowledgeQuotaRemainingBytes 验证剩余空间小于 0 时展示为 0。
func TestKnowledgeQuotaRemainingBytes(t *testing.T) {
	assert.Equal(t, int64(20), knowledgeQuotaRemainingBytes(100, 80))
	assert.Equal(t, int64(0), knowledgeQuotaRemainingBytes(100, 120))
}
