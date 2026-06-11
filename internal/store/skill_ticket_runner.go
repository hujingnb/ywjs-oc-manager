package store

import (
	"context"

	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// skillTicketRunner 把 Store.WithTx 适配成 service.SkillTicketTxRunner,
// 使提交工单与首条需求消息共享同一个数据库事务。
type skillTicketRunner struct {
	// store 持有真实事务入口,runner 只负责把事务查询对象暴露给 service 接口。
	store *Store
}

// NewSkillTicketRunner 创建定制技能工单 service 使用的事务 runner。
func NewSkillTicketRunner(store *Store) *skillTicketRunner {
	return &skillTicketRunner{store: store}
}

// WithSkillTicketTx 在数据库事务中执行工单主表与消息表写操作。
func (r *skillTicketRunner) WithSkillTicketTx(ctx context.Context, fn func(service.SkillTicketStore) error) error {
	return r.store.WithTx(ctx, func(q *sqlc.Queries) error {
		// sqlc.Queries 已实现 service.SkillTicketStore,service 层无需感知事务实现细节。
		return fn(q)
	})
}
