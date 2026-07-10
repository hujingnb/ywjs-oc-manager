package service

import (
	"context"
	"testing"

	null "github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/store/sqlc"
)

// TestAICCRetentionCleanupDeletesExpiredSessions 覆盖保留期清理：
// 过期会话会先删除其图片对象、清空线索最近会话引用，再删除数据库会话，避免对象存储残留访客图片或外键阻塞。
func TestAICCRetentionCleanupDeletesExpiredSessions(t *testing.T) {
	store := &fakeAICCRetentionStore{
		expired: []sqlc.AiccSession{{ID: "session-1"}},
		objects: map[string][]string{
			"session-1": {"apps/app-1/aicc/session-1/file.png"},
		},
	}
	blob := &fakeAICCObjectCleaner{}
	svc := NewAICCRetentionService(store, blob)

	deleted, err := svc.CleanupExpiredSessions(context.Background(), 100)

	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
	assert.Equal(t, []string{"apps/app-1/aicc/session-1/file.png"}, blob.deleted)
	assert.Equal(t, []string{"session-1"}, store.clearedLatestSessions)
	assert.Equal(t, []string{"session-1"}, store.deletedSessions)
}

type fakeAICCRetentionStore struct {
	expired               []sqlc.AiccSession
	objects               map[string][]string
	clearedLatestSessions []string
	deletedSessions       []string
}

func (f *fakeAICCRetentionStore) ListExpiredAICCSessions(ctx context.Context, limit int32) ([]sqlc.AiccSession, error) {
	return f.expired, nil
}

func (f *fakeAICCRetentionStore) ListAICCImageObjectKeysBySession(ctx context.Context, sessionID string) ([]string, error) {
	return f.objects[sessionID], nil
}

func (f *fakeAICCRetentionStore) ClearAICCLeadLatestSession(ctx context.Context, latestSessionID null.String) error {
	f.clearedLatestSessions = append(f.clearedLatestSessions, latestSessionID.String)
	return nil
}

func (f *fakeAICCRetentionStore) DeleteAICCSession(ctx context.Context, id string) error {
	f.deletedSessions = append(f.deletedSessions, id)
	return nil
}

type fakeAICCObjectCleaner struct {
	deleted []string
}

func (f *fakeAICCObjectCleaner) DeleteObject(ctx context.Context, key string) error {
	f.deleted = append(f.deleted, key)
	return nil
}
