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

// aiccRunner 把 Store.WithTx 适配成 service.AICCTxRunner，
// 使管理端整组保存留资字段时停用和 upsert 字段处于同一个事务。
type aiccRunner struct {
	// store 持有真实事务入口，runner 只负责暴露事务查询对象。
	store *Store
}

// aiccDispatcherRunner 为异步客服调度器提供事务边界，确保助手消息与任务完成同步提交。
type aiccDispatcherRunner struct {
	store *Store
}

// NewAICCRunner 创建 AICC 管理 service 使用的事务 runner。
func NewAICCRunner(store *Store) *aiccRunner {
	return &aiccRunner{store: store}
}

// WithAICCTx 在数据库事务中执行 AICC 管理写操作。
func (r *aiccRunner) WithAICCTx(ctx context.Context, fn func(service.AICCStore) error) error {
	return r.store.WithTx(ctx, func(q *sqlc.Queries) error {
		// sqlc.Queries 已实现 service.AICCStore，service 层无需感知事务实现细节。
		return fn(q)
	})
}

// NewAICCPublicRunner 创建 AICC 公开 service 使用的事务 runner。
func NewAICCPublicRunner(store *Store) *aiccPublicRunner {
	return &aiccPublicRunner{store: store}
}

// NewAICCDispatcherRunner 创建 dispatcher 使用的事务 runner。
func NewAICCDispatcherRunner(store *Store) *aiccDispatcherRunner {
	return &aiccDispatcherRunner{store: store}
}

// WithAICCDispatcherTx 在一个事务内保存助手消息并完成持久化任务。
func (r *aiccDispatcherRunner) WithAICCDispatcherTx(ctx context.Context, fn func(service.AICCDispatcherStore) error) error {
	return r.store.WithTx(ctx, func(q *sqlc.Queries) error { return fn(q) })
}

// WithAICCPublicTx 在数据库事务中执行 AICC 公开写操作。
func (r *aiccPublicRunner) WithAICCPublicTx(ctx context.Context, fn func(service.AICCPublicStore) error) error {
	return r.store.WithTx(ctx, func(q *sqlc.Queries) error {
		// sqlc.Queries 已实现 service.AICCPublicStore，service 层无需感知事务实现细节。
		return fn(q)
	})
}
