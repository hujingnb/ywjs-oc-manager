package jobutil

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/store/sqlc"
)

// TestEnsureInitJob_ReplacesExhaustedPendingJob 验证耗尽的 pending 任务不会被 scheduler 永久跳过。
func TestEnsureInitJob_ReplacesExhaustedPendingJob(t *testing.T) {
	store := &initJobStoreStub{job: sqlc.Job{ID: "old", Status: "pending", Attempts: 20955, MaxAttempts: 20}}

	jobID, err := EnsureInitJob(context.Background(), store, "app-1")

	require.NoError(t, err)
	assert.NotEqual(t, "old", jobID)
	require.Len(t, store.created, 1)
	assert.Equal(t, int32(20), store.created[0].MaxAttempts)
}

type initJobStoreStub struct {
	job     sqlc.Job
	created []sqlc.CreateJobParams
}

func (s *initJobStoreStub) GetLatestAppInitJob(context.Context, string) (sqlc.Job, error) {
	if s.job.ID == "" {
		return sqlc.Job{}, sql.ErrNoRows
	}
	return s.job, nil
}

func (s *initJobStoreStub) RequeueJob(context.Context, string) error { return nil }

func (s *initJobStoreStub) CreateJob(_ context.Context, arg sqlc.CreateJobParams) error {
	s.created = append(s.created, arg)
	return nil
}
