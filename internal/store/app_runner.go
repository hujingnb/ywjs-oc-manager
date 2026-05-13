package store

import (
	"context"

	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// appRunner 把 Store.WithTx 适配成 service.AppTxRunner。
type appRunner struct {
	// store 持有真实事务入口，runner 仅负责 service 接口适配。
	store *Store
}

// NewAppRunner 创建实例 service 使用的事务 runner。
func NewAppRunner(store *Store) *appRunner {
	return &appRunner{store: store}
}

// WithAppTx 在数据库事务中执行实例写操作。
func (r *appRunner) WithAppTx(ctx context.Context, fn func(service.AppStore) error) error {
	return r.store.WithTx(ctx, func(q *sqlc.Queries) error {
		// sqlc.Queries 已实现 service.AppStore，避免 app service 感知事务实现细节。
		return fn(q)
	})
}
