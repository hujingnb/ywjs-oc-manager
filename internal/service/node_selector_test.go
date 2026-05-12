// Package service 的 node_selector_test 覆盖运行节点选择器对容量、状态和错误场景的处理。
package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oc-manager/internal/store/sqlc"
)

type sqlNodeSelectorStub struct {
	rows []sqlc.ListActiveNodesWithAppCountsRow
	err  error
}

func (s *sqlNodeSelectorStub) ListActiveNodesWithAppCounts(_ context.Context) ([]sqlc.ListActiveNodesWithAppCountsRow, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.rows, nil
}

// TestSQLNodeSelector_AdaptsRows 验证SQL节点选择器AdaptsRows的预期行为场景。
func TestSQLNodeSelector_AdaptsRows(t *testing.T) {
	id := mustUUID(t, "00000000-0000-0000-0000-000000000a01")
	store := &sqlNodeSelectorStub{rows: []sqlc.ListActiveNodesWithAppCountsRow{{
		ID:       id,
		MaxApps:  pgtype.Int4{Int32: 5, Valid: true},
		AppCount: 2,
	}, {
		ID:       mustUUID(t, "00000000-0000-0000-0000-000000000a02"),
		MaxApps:  pgtype.Int4{}, // NULL
		AppCount: 7,
	}}}
	got, err := NewSQLNodeSelector(store).ListActiveNodesWithAppCounts(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2)
	if got[0].MaxApps == nil || *got[0].MaxApps != 5 || got[0].AppCount != 2 {
		t.Errorf("row[0] = %+v", got[0])
	}
	assert.Nil(t, got[1].MaxApps)
	assert.Equal(t, int64(7), got[1].AppCount)
}

// TestSQLNodeSelector_StoreError 验证SQL节点选择器存储错误的预期行为场景。
func TestSQLNodeSelector_StoreError(t *testing.T) {
	store := &sqlNodeSelectorStub{err: errors.New("db down")}
	_, err := NewSQLNodeSelector(store).ListActiveNodesWithAppCounts(context.Background())
	require.Error(t, err)
}
