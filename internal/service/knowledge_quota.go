package service

import "fmt"

const (
	// KnowledgeQuotaDefaultBytes 是企业和实例知识库的默认累计容量上限（1GB）。
	KnowledgeQuotaDefaultBytes int64 = 1024 * 1024 * 1024
)

// normalizeKnowledgeQuotaBytes 将可选请求值归一为必填正数容量。
// nil 表示调用方未提交容量，创建场景使用 1GB 默认值；更新场景可在调用前选择保留旧值。
func normalizeKnowledgeQuotaBytes(value *int64) (int64, error) {
	if value == nil {
		return KnowledgeQuotaDefaultBytes, nil
	}
	if *value <= 0 {
		return 0, fmt.Errorf("%w: 知识库空间必须大于 0", ErrMemberCreateInvalid)
	}
	return *value, nil
}

// validateKnowledgeQuotaBytes 校验显式提交的容量值。
func validateKnowledgeQuotaBytes(value int64) error {
	if value <= 0 {
		return fmt.Errorf("%w: 知识库空间必须大于 0", ErrMemberCreateInvalid)
	}
	return nil
}

// knowledgeQuotaRemainingBytes 计算前端展示用剩余空间，已超用时按 0 展示。
func knowledgeQuotaRemainingBytes(quotaBytes, usedBytes int64) int64 {
	remaining := quotaBytes - usedBytes
	if remaining < 0 {
		return 0
	}
	return remaining
}
