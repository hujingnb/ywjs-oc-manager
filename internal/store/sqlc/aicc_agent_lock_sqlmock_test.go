package sqlc

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetAICCAgentForUpdateUsesLockingRead 验证资料与知识写事务通过 FOR UPDATE 锁定智能体行，和状态更新形成统一串行边界。
func TestGetAICCAgentForUpdateUsesLockingRead(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	q := New(db)
	assert.Contains(t, normalizedSQL(getAICCAgentForUpdate), "for update")
	mock.ExpectQuery(regexp.QuoteMeta(getAICCAgentForUpdate)).
		WithArgs("agent-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "org_id", "app_id", "name", "status", "scenario", "greeting", "answer_boundary", "privacy_mode", "privacy_text", "retention_days", "theme_json", "allowed_domains_json", "public_token", "widget_token", "created_at", "updated_at", "deleted_at", "persona", "applied_config_revision"}))

	_, err = q.GetAICCAgentForUpdate(context.Background(), "agent-1")

	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
