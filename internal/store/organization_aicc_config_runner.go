package store

import (
	"context"

	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// organizationAICCConfigRunner 把 Store.WithTx 适配成企业 AICC 配置事务 runner。
type organizationAICCConfigRunner struct {
	// store 持有数据库事务入口，runner 只暴露 service 所需的最小接口。
	store *Store
}

// NewOrganizationAICCConfigRunner 创建企业 AICC 配置事务 runner。
func NewOrganizationAICCConfigRunner(store *Store) *organizationAICCConfigRunner {
	return &organizationAICCConfigRunner{store: store}
}

// WithOrganizationAICCConfigTx 在同一事务中执行配置、授权、关联清理和 rollout 任务写入。
func (r *organizationAICCConfigRunner) WithOrganizationAICCConfigTx(ctx context.Context, fn func(service.OrganizationAICCConfigStore) error) error {
	return r.store.WithTx(ctx, func(q *sqlc.Queries) error {
		return fn(q)
	})
}
