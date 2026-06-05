package store

import (
	"context"

	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// assistantVersionRunner 把 Store.WithTx 适配成 service.AssistantVersionTxRunner。
type assistantVersionRunner struct {
	// store 持有真实事务入口，runner 只负责把 sqlc 事务查询对象暴露给 service 接口。
	store *Store
}

// NewAssistantVersionRunner 创建助手版本 service 使用的事务 runner。
func NewAssistantVersionRunner(store *Store) *assistantVersionRunner {
	return &assistantVersionRunner{store: store}
}

// WithAssistantVersionTx 在数据库事务中执行版本和行业库关联写操作。
func (r *assistantVersionRunner) WithAssistantVersionTx(ctx context.Context, fn func(service.AssistantVersionStore) error) error {
	return r.store.WithTx(ctx, func(q *sqlc.Queries) error {
		// sqlc.Queries 已实现 service.AssistantVersionStore，避免 service 感知事务实现细节。
		return fn(q)
	})
}
