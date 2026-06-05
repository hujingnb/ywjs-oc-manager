package store

import (
	"context"

	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// knowledgeRunner 把 Store.WithTx 适配成 service.KnowledgeTxRunner。
type knowledgeRunner struct {
	// store 持有真实事务入口，runner 只做 service 接口适配。
	store *Store
}

// NewKnowledgeRunner 创建知识库 service 使用的事务 runner。
func NewKnowledgeRunner(store *Store) *knowledgeRunner {
	return &knowledgeRunner{store: store}
}

// WithKnowledgeTx 在数据库事务中执行本地知识库写操作。
func (r *knowledgeRunner) WithKnowledgeTx(ctx context.Context, fn func(service.KnowledgeStore) error) error {
	return r.store.WithTx(ctx, func(q *sqlc.Queries) error {
		// sqlc.Queries 已实现 service.KnowledgeStore，避免 service 感知事务实现细节。
		return fn(q)
	})
}
