package webpublish

import (
	"context"
	"testing"
	"time"

	null "github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/store/sqlc"
)

// fakeCertRenewalStore 模拟 CertRenewalStore 接口，返回预置的配置列表。
type fakeCertRenewalStore struct {
	// rows 是 ListConfigsCertExpiringBefore 预置的返回配置列表。
	rows []sqlc.OrgWebPublishConfig
	// gotBefore 记录最近一次调用收到的阈值参数，供断言。
	gotBefore null.Time
}

// ListConfigsCertExpiringBefore 返回预置的 rows，记录阈值参数供断言。
func (f *fakeCertRenewalStore) ListConfigsCertExpiringBefore(_ context.Context, before null.Time) ([]sqlc.OrgWebPublishConfig, error) {
	f.gotBefore = before
	return f.rows, nil
}

// fakeProvisionEnqueuer 模拟 ProvisionEnqueuer 接口，记录入队的 orgID 列表。
type fakeProvisionEnqueuer struct {
	// enqueuedOrgIDs 记录每次 EnqueueProvision 收到的 orgID。
	enqueuedOrgIDs []string
}

// EnqueueProvision 记录 orgID，返回 nil 模拟成功。
func (f *fakeProvisionEnqueuer) EnqueueProvision(_ context.Context, orgID string) error {
	f.enqueuedOrgIDs = append(f.enqueuedOrgIDs, orgID)
	return nil
}

// TestCheckOnceEnqueuesExpiring 覆盖：store 返回 2 条临近到期配置时，
// CheckOnce 应为每条配置各调用一次 EnqueueProvision，且传入正确的 orgID。
func TestCheckOnceEnqueuesExpiring(t *testing.T) {
	// 预置两条临近到期的配置
	store := &fakeCertRenewalStore{
		rows: []sqlc.OrgWebPublishConfig{
			{OrgID: "org-A"},
			{OrgID: "org-B"},
		},
	}
	enqueuer := &fakeProvisionEnqueuer{}

	// 固定时钟：2026-06-30 00:00:00 UTC，renewBefore=30 天
	fixedNow := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	checker := NewCertRenewalChecker(store, enqueuer, 30*24*time.Hour, func() time.Time { return fixedNow })

	// 执行一轮巡检，期望无错误
	require.NoError(t, checker.CheckOnce(context.Background()))

	// 验证阈值计算正确：now() + renewBefore = 2026-07-30 00:00:00 UTC
	expectedThreshold := fixedNow.Add(30 * 24 * time.Hour)
	require.True(t, store.gotBefore.Valid, "阈值时间应为有效 null.Time")
	assert.Equal(t, expectedThreshold.UTC(), store.gotBefore.Time.UTC(),
		"阈值应为 now()+renewBefore")

	// 验证两条配置都被入队
	assert.Len(t, enqueuer.enqueuedOrgIDs, 2, "应为 2 条配置各入队一次")
	assert.Contains(t, enqueuer.enqueuedOrgIDs, "org-A", "org-A 应被入队")
	assert.Contains(t, enqueuer.enqueuedOrgIDs, "org-B", "org-B 应被入队")
}
