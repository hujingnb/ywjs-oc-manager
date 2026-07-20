package sqlc

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListOrganizationIndustryKnowledgeBasesForUpdateUsesLockingRead 验证 AICC 写事务通过
// FOR UPDATE 锁住企业授权行，使平台整组 DELETE 必须与 agent 关联写入串行。
func TestListOrganizationIndustryKnowledgeBasesForUpdateUsesLockingRead(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	q := New(db)
	assert.Contains(t, normalizedSQL(listOrganizationIndustryKnowledgeBasesForUpdate), "for update")
	mock.ExpectQuery(regexp.QuoteMeta(listOrganizationIndustryKnowledgeBasesForUpdate)).
		WithArgs("org-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "created_by", "created_at", "updated_at", "deleted_at", "name_active_key"}))

	rows, err := q.ListOrganizationIndustryKnowledgeBasesForUpdate(context.Background(), "org-1")

	require.NoError(t, err)
	assert.Empty(t, rows)
	require.NoError(t, mock.ExpectationsWereMet())
}
