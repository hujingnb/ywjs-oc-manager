package service

import (
	"context"

	"oc-manager/internal/store/sqlc"
)

// SQLNodeSelectorStore 是 SQLNodeSelector 的最小依赖；对应 sqlc 生成的查询方法。
// 把 store 接口与 NodeSelector 接口拆开，让 service 层只在 wiring 时绑定 sqlc，
// 其它单测可继续注入内存桩 NodeSelector。
type SQLNodeSelectorStore interface {
	ListActiveNodesWithAppCounts(ctx context.Context) ([]sqlc.ListActiveNodesWithAppCountsRow, error)
}

// SQLNodeSelector 把 sqlc 生成的扁平 row 翻译成 NodeWithCount，供 OnboardingService 自动选节点。
type SQLNodeSelector struct {
	store SQLNodeSelectorStore
}

// NewSQLNodeSelector 构造一个绑定到 sqlc store 的 NodeSelector。
func NewSQLNodeSelector(store SQLNodeSelectorStore) *SQLNodeSelector {
	return &SQLNodeSelector{store: store}
}

// ListActiveNodesWithAppCounts 实现 NodeSelector 接口。
func (s *SQLNodeSelector) ListActiveNodesWithAppCounts(ctx context.Context) ([]NodeWithCount, error) {
	rows, err := s.store.ListActiveNodesWithAppCounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]NodeWithCount, 0, len(rows))
	for _, r := range rows {
		nc := NodeWithCount{
			// ID 已是 string，直接使用。
			NodeID:   r.ID,
			AppCount: r.AppCount,
		}
		if r.MaxApps.Valid {
			// null.Int 内部是 int64；NodeWithCount.MaxApps 是 *int32。
			v := int32(r.MaxApps.Int64)
			nc.MaxApps = &v
		}
		out = append(out, nc)
	}
	return out, nil
}
