package store

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// TestOrganizationAICCConfigRunnerRollsBackWhenJobInsertFails 验证任务写入失败时事务 rollback 且绝不 commit。
func TestOrganizationAICCConfigRunnerRollsBackWhenJobInsertFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	runner := NewOrganizationAICCConfigRunner(New(db))
	jobErr := errors.New("insert job failed")
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO jobs (")).
		WithArgs("job-1", "aicc_model_rollout", int32(100), sqlmock.AnyArg(), int32(20), []byte(`{"org_id":"org-1","target_revision":8}`)).
		WillReturnError(jobErr)
	mock.ExpectRollback()

	err = runner.WithOrganizationAICCConfigTx(context.Background(), func(store service.OrganizationAICCConfigStore) error {
		return store.CreateJob(context.Background(), sqlc.CreateJobParams{
			ID: "job-1", Type: "aicc_model_rollout", Priority: 100, RunAfter: time.Now().UTC(), MaxAttempts: 20,
			PayloadJson: []byte(`{"org_id":"org-1","target_revision":8}`),
		})
	})

	require.ErrorIs(t, err, jobErr)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestAICCPlatformPromptRolloutRunnerLocksGuardBeforeCallback 验证协调器的检查和创建闭包运行前已在同一事务中锁定 singleton guard。
func TestAICCPlatformPromptRolloutRunnerLocksGuardBeforeCallback(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	runner := NewAICCPlatformPromptRolloutRunner(New(db))
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT singleton\nFROM aicc_platform_prompt_rollout_guards\nWHERE singleton = 1\nFOR UPDATE")).
		WillReturnRows(sqlmock.NewRows([]string{"singleton"}).AddRow(1))
	mock.ExpectCommit()

	err = runner.WithAICCPlatformPromptRolloutTx(context.Background(), func(service.AICCPlatformPromptRolloutStore) error {
		// callback 代表活跃任务检查、落后检查和创建任务，必须在 guard 加锁后执行。
		return nil
	})

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
