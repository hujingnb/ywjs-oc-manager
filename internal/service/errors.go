package service

import "errors"

var (
	ErrForbidden = errors.New("无权执行该操作")
	ErrNotFound  = errors.New("资源不存在")
	// ErrNoNodeAvailable 表示当前没有「active 且剩余容量 > 0」的节点可分配新应用。
	// 由 OnboardingService 在自动选节点失败时返回；handler 层映射为 503 + NO_NODE_AVAILABLE。
	ErrNoNodeAvailable = errors.New("当前无可用 runtime 节点")
)
