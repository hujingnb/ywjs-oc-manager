package store

import "oc-manager/internal/store/sqlc"

// runtimeNodeStore 目前只组合 sqlc.Queries。
//
// 自动注册切换后，旧 bootstrap rotate 的裸 SQL 已移除；保留这个包装类型是为了让
// cmd/server 的装配点稳定，后续若需要事务或扩展查询仍可在这里集中补齐。
type runtimeNodeStore struct {
	// Queries 继承 sqlc 生成的运行节点查询方法，保持 service 依赖接口稳定。
	*sqlc.Queries
}

// NewRuntimeNodeStore 用现有 Store 构造 service.RuntimeNodeStore 实现。
func NewRuntimeNodeStore(s *Store) *runtimeNodeStore {
	return &runtimeNodeStore{Queries: s.Queries}
}
