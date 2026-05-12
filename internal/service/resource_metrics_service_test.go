// Package service 的 resource_metrics_service_test 覆盖资源指标查询服务的权限与参数边界。
package service

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

const (
	testResourceOrgID      = "00000000-0000-0000-0000-00000000e001"
	testResourceOwnerID    = "00000000-0000-0000-0000-00000000e002"
	testResourceMemberID   = "00000000-0000-0000-0000-00000000e003"
	testResourceAppID      = "00000000-0000-0000-0000-00000000e004"
	testResourceRuntimeID  = "00000000-0000-0000-0000-00000000e005"
	testResourceContainer  = "openclaw-resource-test"
	testResourceSampleTime = "2026-05-13T12:00:00Z"
)

// TestResourceMetricsServiceListAppResourcesRequiresViewPermission 验证应用资源趋势拒绝同组织非 owner 成员读取他人应用的异常路径。
func TestResourceMetricsServiceListAppResourcesRequiresViewPermission(t *testing.T) {
	store := newResourceMetricsStoreStub(t)
	store.app = sqlc.App{
		ID:            mustUUID(t, testResourceAppID),
		OrgID:         mustUUID(t, testResourceOrgID),
		OwnerUserID:   mustUUID(t, testResourceOwnerID),
		RuntimeNodeID: mustUUID(t, testResourceRuntimeID),
		Name:          "测试应用",
		Status:        domain.AppStatusRunning,
	}
	svc := NewResourceMetricsService(store)

	_, err := svc.ListAppResources(context.Background(), auth.Principal{
		Role:   domain.UserRoleOrgMember,
		OrgID:  testResourceOrgID,
		UserID: testResourceMemberID,
	}, testResourceAppID, ResourceTimeRange{})
	require.ErrorIs(t, err, ErrForbidden)
}

// TestResourceMetricsServiceRejectsInvalidBucket 验证资源时间范围解析拒绝不支持的 bucket 粒度。
func TestResourceMetricsServiceRejectsInvalidBucket(t *testing.T) {
	fixedNow := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)

	_, err := NormalizeResourceRange("", "", "2m", fixedNow)
	require.ErrorIs(t, err, ErrInvalidResourceRange)
}

// TestResourceMetricsServiceListNodeInstanceResourcesRequiresNodeBinding 验证节点实例资源趋势拒绝查询不属于该节点的应用。
func TestResourceMetricsServiceListNodeInstanceResourcesRequiresNodeBinding(t *testing.T) {
	store := newResourceMetricsStoreStub(t)
	store.app = sqlc.App{
		ID:            mustUUID(t, testResourceAppID),
		OrgID:         mustUUID(t, testResourceOrgID),
		OwnerUserID:   mustUUID(t, testResourceOwnerID),
		RuntimeNodeID: mustUUID(t, "00000000-0000-0000-0000-00000000e006"),
		Name:          "其他节点应用",
		Status:        domain.AppStatusRunning,
	}
	svc := NewResourceMetricsService(store)

	_, err := svc.ListNodeInstanceResources(context.Background(), auth.Principal{
		Role: domain.UserRolePlatformAdmin,
	}, testResourceRuntimeID, testResourceAppID, ResourceTimeRange{})
	require.ErrorIs(t, err, ErrNotFound)
}

type resourceMetricsStoreStub struct {
	t   *testing.T
	app sqlc.App
}

func newResourceMetricsStoreStub(t *testing.T) *resourceMetricsStoreStub {
	t.Helper()
	return &resourceMetricsStoreStub{t: t}
}

func (s *resourceMetricsStoreStub) GetRuntimeNode(_ context.Context, id pgtype.UUID) (sqlc.RuntimeNode, error) {
	if uuidToString(id) != testResourceRuntimeID {
		return sqlc.RuntimeNode{}, pgx.ErrNoRows
	}
	return sqlc.RuntimeNode{ID: id, Name: "资源测试节点", Status: domain.RuntimeNodeStatusActive}, nil
}

func (s *resourceMetricsStoreStub) GetApp(_ context.Context, id pgtype.UUID) (sqlc.App, error) {
	if uuidToString(id) != uuidToString(s.app.ID) {
		return sqlc.App{}, pgx.ErrNoRows
	}
	return s.app, nil
}

func (s *resourceMetricsStoreStub) ListAppsByRuntimeNode(context.Context, sqlc.ListAppsByRuntimeNodeParams) ([]sqlc.App, error) {
	return []sqlc.App{s.app}, nil
}

func (s *resourceMetricsStoreStub) ListLatestInstanceResourceSamplesByNode(context.Context, pgtype.UUID) ([]sqlc.InstanceResourceSample, error) {
	return nil, nil
}

func (s *resourceMetricsStoreStub) ListNodeResourceSamples(context.Context, sqlc.ListNodeResourceSamplesParams) ([]sqlc.NodeResourceSample, error) {
	return nil, nil
}

func (s *resourceMetricsStoreStub) ListNodeResourceBuckets(context.Context, sqlc.ListNodeResourceBucketsParams) ([]sqlc.ListNodeResourceBucketsRow, error) {
	return nil, nil
}

func (s *resourceMetricsStoreStub) ListNodeInstanceResourceSamples(context.Context, sqlc.ListNodeInstanceResourceSamplesParams) ([]sqlc.InstanceResourceSample, error) {
	return nil, nil
}

func (s *resourceMetricsStoreStub) ListNodeInstanceResourceBuckets(context.Context, sqlc.ListNodeInstanceResourceBucketsParams) ([]sqlc.ListNodeInstanceResourceBucketsRow, error) {
	return nil, nil
}

func (s *resourceMetricsStoreStub) ListInstanceResourceSamples(context.Context, sqlc.ListInstanceResourceSamplesParams) ([]sqlc.InstanceResourceSample, error) {
	return []sqlc.InstanceResourceSample{{
		AppID:           s.app.ID,
		RuntimeNodeID:   s.app.RuntimeNodeID,
		ContainerID:     testResourceContainer,
		SampledAt:       pgtype.Timestamptz{Time: mustTime(s.t, testResourceSampleTime), Valid: true},
		ContainerStatus: pgtype.Text{String: domain.AppStatusRunning, Valid: true},
	}}, nil
}

func (s *resourceMetricsStoreStub) ListInstanceResourceBuckets(context.Context, sqlc.ListInstanceResourceBucketsParams) ([]sqlc.ListInstanceResourceBucketsRow, error) {
	return nil, nil
}
