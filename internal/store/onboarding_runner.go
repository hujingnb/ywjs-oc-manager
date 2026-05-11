package store

import (
	"context"

	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// onboardingRunner 把 Store.WithTx 适配成 service.TxRunner，
// 使 service 层不必直接依赖 *sqlc.Queries 的事务 API。
type onboardingRunner struct {
	// store 持有真实事务入口，runner 仅负责 service 接口适配。
	store *Store
}

// NewOnboardingRunner 创建 service.TxRunner 实现。
func NewOnboardingRunner(store *Store) *onboardingRunner {
	return &onboardingRunner{store: store}
}

// WithTx 在数据库事务中调用 fn。
// 任意一步失败都会触发整事务回滚，调用方仅需关心业务错误。
func (r *onboardingRunner) WithTx(ctx context.Context, fn func(service.OnboardingStore) error) error {
	return r.store.WithTx(ctx, func(q *sqlc.Queries) error {
		// sqlc.Queries 已实现 service.OnboardingStore，避免 onboarding service 感知事务实现细节。
		return fn(q)
	})
}
