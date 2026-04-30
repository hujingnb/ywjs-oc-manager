package domain

import "fmt"

// JobTransition 描述一次 job 状态切换。
// 任意状态变更必须显式列出 from→to，避免在散落的 SQL 里改写状态时漏掉非法路径。
type JobTransition struct {
	From string
	To   string
}

var jobTransitions = map[JobTransition]struct{}{
	{From: JobStatusPending, To: JobStatusRunning}:    {},
	{From: JobStatusPending, To: JobStatusCanceled}:   {},
	{From: JobStatusRunning, To: JobStatusSucceeded}:  {},
	{From: JobStatusRunning, To: JobStatusFailed}:     {},
	{From: JobStatusRunning, To: JobStatusPending}:    {}, // 重新排队
	{From: JobStatusFailed, To: JobStatusPending}:     {}, // 手工重试
}

// IsJobTransitionAllowed 校验 job 状态切换是否合法。
// 用 map 而不是 switch 是为了便于在测试中遍历完整的允许集。
func IsJobTransitionAllowed(from, to string) bool {
	if from == to {
		return false
	}
	_, ok := jobTransitions[JobTransition{From: from, To: to}]
	return ok
}

// EnsureJobTransition 在不允许的状态切换上返回错误，便于 service 层直接对外报告。
func EnsureJobTransition(from, to string) error {
	if !IsJobTransitionAllowed(from, to) {
		return fmt.Errorf("非法 job 状态转移: %s -> %s", from, to)
	}
	return nil
}

// JobIsTerminal 判断 job 是否已经进入终态，调度器据此决定是否可以从队列中移除。
func JobIsTerminal(status string) bool {
	switch status {
	case JobStatusSucceeded, JobStatusFailed, JobStatusCanceled:
		return true
	default:
		return false
	}
}

// AllowedJobTransitions 暴露当前允许的状态转移集合，主要供测试和文档使用。
func AllowedJobTransitions() []JobTransition {
	results := make([]JobTransition, 0, len(jobTransitions))
	for t := range jobTransitions {
		results = append(results, t)
	}
	return results
}
