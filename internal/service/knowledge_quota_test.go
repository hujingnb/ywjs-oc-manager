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

// TestKnowledgeQuotaRemainingBytes 验证剩余空间小于 0 时展示为 0。
func TestKnowledgeQuotaRemainingBytes(t *testing.T) {
	assert.Equal(t, int64(20), knowledgeQuotaRemainingBytes(100, 80))
	assert.Equal(t, int64(0), knowledgeQuotaRemainingBytes(100, 120))
}
