package store

import (
	"context"

	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// aiccPublicRunner 把 Store.WithTx 适配成 service.AICCPublicTxRunner，
// 使公开留资提交的多字段写入与会话状态更新共享同一个数据库事务。
type aiccPublicRunner struct {
	// store 持有真实事务入口，runner 只负责把事务查询对象暴露给 service 接口。
	store *Store
}

// NewAICCPublicRunner 创建 AICC 公开 service 使用的事务 runner。
func NewAICCPublicRunner(store *Store) *aiccPublicRunner {
	return &aiccPublicRunner{store: store}
}

// WithAICCPublicTx 在数据库事务中执行 AICC 公开写操作。
func (r *aiccPublicRunner) WithAICCPublicTx(ctx context.Context, fn func(service.AICCPublicStore) error) error {
	return r.store.WithTx(ctx, func(q *sqlc.Queries) error {
		// sqlc.Queries 已实现 service.AICCPublicStore，service 层无需感知事务实现细节。
		return fn(q)
	})
}
