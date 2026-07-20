package store

import (
	"context"
	"fmt"

	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// aiccPlatformPromptRolloutRunner 把 Store.WithTx 适配为全局平台提示词发布所需的 guard 事务。
type aiccPlatformPromptRolloutRunner struct {
	store *Store
}

// NewAICCPlatformPromptRolloutRunner 创建启动协调器使用的事务 runner。
func NewAICCPlatformPromptRolloutRunner(store *Store) *aiccPlatformPromptRolloutRunner {
	return &aiccPlatformPromptRolloutRunner{store: store}
}

// WithAICCPlatformPromptRolloutTx 先锁定 singleton guard 行，再在同一事务内执行检查和任务创建。
func (r *aiccPlatformPromptRolloutRunner) WithAICCPlatformPromptRolloutTx(ctx context.Context, fn func(service.AICCPlatformPromptRolloutStore) error) error {
	return r.store.WithTx(ctx, func(q *sqlc.Queries) error {
		if _, err := q.LockAICCPlatformPromptRolloutGuard(ctx); err != nil {
			return fmt.Errorf("锁定 AICC 平台提示词发布 guard 失败: %w", err)
		}
		return fn(q)
	})
}
