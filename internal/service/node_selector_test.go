package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

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
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].MaxApps == nil || *got[0].MaxApps != 5 || got[0].AppCount != 2 {
		t.Errorf("row[0] = %+v", got[0])
	}
	if got[1].MaxApps != nil {
		t.Errorf("row[1].MaxApps should be nil for NULL, got %v", *got[1].MaxApps)
	}
	if got[1].AppCount != 7 {
		t.Errorf("row[1].AppCount = %d, want 7", got[1].AppCount)
	}
}

func TestSQLNodeSelector_StoreError(t *testing.T) {
	store := &sqlNodeSelectorStub{err: errors.New("db down")}
	_, err := NewSQLNodeSelector(store).ListActiveNodesWithAppCounts(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}
