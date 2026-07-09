package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	null "github.com/guregu/null/v5"

	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// newDiscardLogger 返回丢弃所有输出的测试用 logger，避免测试日志污染。
func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

const (
	testRuntimeOpAppID = "00000000-0000-0000-0000-000000001001"
	testRuntimeOpOrg   = "00000000-0000-0000-0000-000000001002"
	testRuntimeOpOwner = "00000000-0000-0000-0000-000000001003"
)

// TestRuntimeOperationTriggersJobAndAudit 验证运行时OperationTriggers任务并审计的预期行为场景。
func TestRuntimeOperationTriggersJobAndAudit(t *testing.T) {
	store := newRuntimeOperationStub(t)
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	result, err := svc.Trigger(context.Background(), runtimeOrgAdminPrincipal(), testRuntimeOpAppID, RuntimeOperationStart)
	require.NoError(t, err)
	require.NotEmpty(t, result.JobID)
	require.Equal(t, RuntimeOperationStart, result.Operation)
	require.Equal(t, domain.JobTypeAppStartContainer, store.lastJobType)
	require.True(t, store.auditWritten)
}

// TestRuntimeOperationDeniesOtherOrg 验证运行时OperationDenies其他组织的预期行为场景。
func TestRuntimeOperationDeniesOtherOrg(t *testing.T) {
	store := newRuntimeOperationStub(t)
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	_, err := svc.Trigger(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "another-org"}, testRuntimeOpAppID, RuntimeOperationStop)
	require.ErrorIs(t, err, ErrRuntimeOperationDenied)
}

// TestRuntimeOperationRejectsUnknown 验证运行时Operation拒绝未知的异常或拒绝路径场景。
func TestRuntimeOperationRejectsUnknown(t *testing.T) {
	store := newRuntimeOperationStub(t)
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	_, err := svc.Trigger(context.Background(), platformAdmin(), testRuntimeOpAppID, "boom")
	require.Error(t, err)
}

// TestRuntimeOperationEnqueuesNotifierWhenProvided 验证运行时OperationEnqueuesNotifier当Provided的预期行为场景。
func TestRuntimeOperationEnqueuesNotifierWhenProvided(t *testing.T) {
	store := newRuntimeOperationStub(t)
	notifier := &fakeNotifier{}
	svc := NewRuntimeOperationService(store, newDiscardLogger(), notifier)

	result, err := svc.Trigger(context.Background(), runtimeOrgAdminPrincipal(), testRuntimeOpAppID, RuntimeOperationStop)
	require.NoError(t, err)
	require.Equal(t, result.JobID, notifier.lastJobID)
}

// TestRuntimeOperationSurvivesNotifierError 验证运行时OperationSurvivesNotifier错误的预期行为场景。
func TestRuntimeOperationSurvivesNotifierError(t *testing.T) {
	store := newRuntimeOperationStub(t)
	notifier := &fakeNotifier{err: errors.New("redis down")}
	svc := NewRuntimeOperationService(store, newDiscardLogger(), notifier)

	_, err := svc.Trigger(context.Background(), runtimeOrgAdminPrincipal(), testRuntimeOpAppID, RuntimeOperationStop)
	require.NoError(t, err)
}

// TestRuntimeOperationAllowsPlatformAdmin 验证 CanTriggerRuntimeOperation 扩展后平台管理员可触发 stop 等运行时操作。
func TestRuntimeOperationAllowsPlatformAdmin(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.Status = domain.AppStatusRunning
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	result, err := svc.Trigger(context.Background(), platformAdmin(), testRuntimeOpAppID, RuntimeOperationStop)
	require.NoError(t, err, "CanTriggerRuntimeOperation 已扩展至 platform_admin，Trigger 应允许")
	require.NotEmpty(t, result.JobID, "成功触发后应返回非空 job_id")
}

// TestRuntimeOperationRejectsAICCHiddenApp 覆盖普通运行时入口隔离：AICC 隐藏 app 不允许通过
// 普通实例启停、运行态查看或重新初始化入口操作，避免绕过 AICC 管理语义。
func TestRuntimeOperationRejectsAICCHiddenApp(t *testing.T) {
	// 子场景：InspectApp 不暴露 hidden app 运行态。
	t.Run("运行态查看返回不存在", func(t *testing.T) {
		store := newRuntimeOperationStub(t)
		store.app.AiccHidden = true
		svc := NewRuntimeOperationService(store, newDiscardLogger())

		_, err := svc.InspectApp(context.Background(), runtimeOrgAdminPrincipal(), testRuntimeOpAppID)

		require.ErrorIs(t, err, ErrNotFound)
	})

	// 子场景：Trigger 不允许对 hidden app 入队启停重启删除任务。
	t.Run("运行操作返回不存在", func(t *testing.T) {
		store := newRuntimeOperationStub(t)
		store.app.AiccHidden = true
		svc := NewRuntimeOperationService(store, newDiscardLogger())

		_, err := svc.Trigger(context.Background(), runtimeOrgAdminPrincipal(), testRuntimeOpAppID, RuntimeOperationRestart)

		require.ErrorIs(t, err, ErrNotFound)
	})

	// 子场景：RequestInitialize 不允许通过普通实例入口重跑 hidden app 初始化。
	t.Run("重新初始化返回不存在", func(t *testing.T) {
		store := newRuntimeOperationStub(t)
		store.app.AiccHidden = true
		store.app.Status = domain.AppStatusError
		svc := NewRuntimeOperationService(store, newDiscardLogger())

		_, err := svc.RequestInitialize(context.Background(), runtimeOrgAdminPrincipal(), testRuntimeOpAppID)

		require.ErrorIs(t, err, ErrNotFound)
	})
}

// TestRequestInitialize_HappyPathFromError 验证请求初始化成功路径来自错误的成功路径场景。
// spec-A2b：container_id / runtime_node_id 已从 schema 删除，不再在此测试中设置。
func TestRequestInitialize_HappyPathFromError(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.Status = domain.AppStatusError
	store.app.ApiKeyStatus = domain.APIKeyStatusError
	notifier := &fakeNotifier{}
	svc := NewRuntimeOperationService(store, newDiscardLogger(), notifier)

	result, err := svc.RequestInitialize(context.Background(), runtimeOrgAdminPrincipal(), testRuntimeOpAppID)
	require.NoError(t, err)
	require.NotEmpty(t, result.JobID)
	require.Equal(t, RuntimeOperation("initialize"), result.Operation)
	// 重置目标为 pulling_runtime_image，worker 直接从第一阶段开始重跑。
	require.Equal(t, domain.AppStatusPullingRuntimeImage, store.app.Status)
	require.Equal(t, domain.APIKeyStatusPending, store.app.ApiKeyStatus)
	// ClearAppProgress 必须被调用，否则前端会看到上一次失败遗留的进度数。
	require.True(t, store.progressCleared)
	require.Equal(t, domain.JobTypeAppInitialize, store.lastJobType)
	require.True(t, store.auditWritten)
	require.Equal(t, result.JobID, notifier.lastJobID)
}

// TestRequestInitialize_RejectsRunningStatus 验证请求初始化拒绝Running状态的异常或拒绝路径场景。
func TestRequestInitialize_RejectsRunningStatus(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.Status = domain.AppStatusRunning
	svc := NewRuntimeOperationService(store, newDiscardLogger())
	_, err := svc.RequestInitialize(context.Background(), runtimeOrgAdminPrincipal(), testRuntimeOpAppID)
	require.ErrorIs(t, err, ErrAppNotReinitializable)
}

// TestRequestInitialize_AllowsPlatformAdmin 验证平台管理员可运维介入重新初始化：
// CanManageApp 放开 platform_admin 后不再返回 ErrRuntimeOperationDenied，error 态实例可被重置。
func TestRequestInitialize_AllowsPlatformAdmin(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.Status = domain.AppStatusError
	notifier := &fakeNotifier{}
	svc := NewRuntimeOperationService(store, newDiscardLogger(), notifier)

	result, err := svc.RequestInitialize(context.Background(), platformAdmin(), testRuntimeOpAppID)
	require.NoError(t, err)
	require.NotEmpty(t, result.JobID)
	require.Equal(t, domain.AppStatusPullingRuntimeImage, store.app.Status)
}

// TestInspectApp_ReturnsDBStatus 验证 InspectApp 直接返回库内 apps.status，不经 docker inspect。
// spec-A2b：k8s 下运行态由 status reconciler 写入，InspectApp 以库内状态为权威来源。
func TestInspectApp_ReturnsDBStatus(t *testing.T) {
	// 构造 status=running 的应用，验证 InspectApp 直接透传库内状态。
	store := newRuntimeOperationStub(t)
	store.app.Status = domain.AppStatusRunning
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	view, err := svc.InspectApp(context.Background(), platformAdmin(), testRuntimeOpAppID)
	require.NoError(t, err)
	// 应返回库内 status，不依赖 docker inspect。
	require.Equal(t, domain.AppStatusRunning, view.Status)
	// spec-A2b：Container 字段已彻底删除，RuntimeView 仅含 Status + Snapshot。
	require.Nil(t, view.Snapshot)
}

// TestInspectApp_ReturnsSnapshotFromDB 验证 InspectApp 返回 runtime_snapshot_json 中的快照数据。
// k8s 下快照由 status reconciler 周期写入 apps.runtime_snapshot_json，InspectApp 直接解析返回。
func TestInspectApp_ReturnsSnapshotFromDB(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.Status = domain.AppStatusRunning
	// 构造合法的 runtime_snapshot_json，验证 snapshotFromApp 能正确解析并返回。
	store.app.RuntimeSnapshotJson = []byte(`{"cpu_percent":12.5,"memory_usage_bytes":1048576,"memory_limit_bytes":2097152,"network_rx_bytes":100,"network_tx_bytes":200}`)
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	view, err := svc.InspectApp(context.Background(), platformAdmin(), testRuntimeOpAppID)
	require.NoError(t, err)
	require.Equal(t, domain.AppStatusRunning, view.Status)
	// 快照应被解析并返回，CPU/内存字段应与 JSON 一致。
	require.NotNil(t, view.Snapshot, "runtime_snapshot_json 非空时应返回 Snapshot")
	require.InDelta(t, 12.5, view.Snapshot.CPUPercent, 0.001)
	require.Equal(t, uint64(1048576), view.Snapshot.MemoryUsage)
}

// TestInspectApp_ForbiddenForOtherOrg 验证 InspectApp 对非本组织主体拒绝访问。
// 权限判断依赖 auth.CanViewApp，非本组织 admin 不得查看他组织的应用运行时视图。
func TestInspectApp_ForbiddenForOtherOrg(t *testing.T) {
	store := newRuntimeOperationStub(t)
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	// 用其他组织的 admin 身份发起请求，应返回 ErrForbidden。
	otherPrincipal := auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "other-org"}
	_, err := svc.InspectApp(context.Background(), otherPrincipal, testRuntimeOpAppID)
	require.ErrorIs(t, err, ErrForbidden)
}

// TestInspectApp_NotFoundForMissingApp 验证 InspectApp 对不存在的 appID 返回 ErrNotFound。
func TestInspectApp_NotFoundForMissingApp(t *testing.T) {
	store := newRuntimeOperationStub(t)
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	// 传入不存在的 appID，store.GetApp 返回 sql.ErrNoRows，应映射为 ErrNotFound。
	_, err := svc.InspectApp(context.Background(), platformAdmin(), "00000000-0000-0000-0000-000000000000")
	require.ErrorIs(t, err, ErrNotFound)
}

// TestRequestInitialize_DeniesOtherOrg 验证请求初始化Denies其他组织的预期行为场景。
func TestRequestInitialize_DeniesOtherOrg(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.Status = domain.AppStatusError
	svc := NewRuntimeOperationService(store, newDiscardLogger())
	_, err := svc.RequestInitialize(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "other"}, testRuntimeOpAppID)
	require.ErrorIs(t, err, ErrRuntimeOperationDenied)
}

// TestRuntimeOperationMembersCanOnlyTriggerOwnApp 验证运行时Operation成员权限判断仅触发本人应用的预期行为场景。
func TestRuntimeOperationMembersCanOnlyTriggerOwnApp(t *testing.T) {
	store := newRuntimeOperationStub(t)
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	if _, err := svc.Trigger(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testRuntimeOpOrg, UserID: "stranger"}, testRuntimeOpAppID, RuntimeOperationRestart); !errors.Is(err, ErrRuntimeOperationDenied) {
		t.Fatalf("error = %v, want ErrRuntimeOperationDenied", err)
	}

	_, err := svc.Trigger(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testRuntimeOpOrg, UserID: testRuntimeOpOwner}, testRuntimeOpAppID, RuntimeOperationRestart)
	require.NoError(t, err)
}

func runtimeOrgAdminPrincipal() auth.Principal {
	return auth.Principal{
		Role:   domain.UserRoleOrgAdmin,
		OrgID:  testRuntimeOpOrg,
		UserID: "00000000-0000-0000-0000-0000000010aa",
	}
}

type runtimeOperationStub struct {
	t   *testing.T
	app sqlc.App
	// userStatus 控制 GetUser 返回的用户状态；默认为 active。
	userStatus   string
	lastJobType  string
	auditWritten bool
	// progressCleared 标记 ClearAppProgress 被调用过。
	progressCleared bool
	// channelBindingCount 控制 CountChannelBindingsByApp 返回的渠道绑定数；默认 0。
	channelBindingCount int64
	// lastAuditMeta 记录最近一次 CreateAuditLog 传入的 MetadataJson，供断言使用。
	lastAuditMeta []byte
}

func newRuntimeOperationStub(t *testing.T) *runtimeOperationStub {
	app := sqlc.App{
		ID:           mustUUID(t, testRuntimeOpAppID),
		OrgID:        mustUUID(t, testRuntimeOpOrg),
		OwnerUserID:  mustUUID(t, testRuntimeOpOwner),
		Status:       domain.AppStatusRunning,
		ApiKeyStatus: domain.APIKeyStatusActive,
	}
	return &runtimeOperationStub{t: t, app: app, userStatus: domain.StatusActive}
}

func (s *runtimeOperationStub) GetApp(_ context.Context, id string) (sqlc.App, error) {
	if id != s.app.ID {
		return sqlc.App{}, fakeNotFound
	}
	return s.app, nil
}

func (s *runtimeOperationStub) GetUser(_ context.Context, _ string) (sqlc.User, error) {
	return sqlc.User{Status: s.userStatus}, nil
}

// CreateJob 为 :exec；stub 记录任务类型并生成一个固定 ID。
func (s *runtimeOperationStub) CreateJob(_ context.Context, arg sqlc.CreateJobParams) error {
	s.lastJobType = arg.Type
	return nil
}

// CreateAuditLog 为 :exec；stub 记录是否写入及结构化元数据字段。
func (s *runtimeOperationStub) CreateAuditLog(_ context.Context, arg sqlc.CreateAuditLogParams) error {
	s.auditWritten = true
	s.lastAuditMeta = arg.MetadataJson
	return nil
}

// CountChannelBindingsByApp 返回 channelBindingCount，模拟查询结果。
func (s *runtimeOperationStub) CountChannelBindingsByApp(_ context.Context, _ string) (int64, error) {
	return s.channelBindingCount, nil
}

// SetAppStatus 为 :exec；stub 直接更新内存中的 app.Status。
func (s *runtimeOperationStub) SetAppStatus(_ context.Context, arg sqlc.SetAppStatusParams) error {
	s.app.Status = arg.Status
	return nil
}

// ClearAppProgress 为 :exec；stub 清空进度字段并记录调用。
func (s *runtimeOperationStub) ClearAppProgress(_ context.Context, _ string) error {
	s.app.ProgressCurrent = null.Int{}
	s.app.ProgressTotal = null.Int{}
	s.progressCleared = true
	return nil
}

// SetAppNewAPIKey 为 :exec；stub 更新 api key 相关字段。
func (s *runtimeOperationStub) SetAppNewAPIKey(_ context.Context, arg sqlc.SetAppNewAPIKeyParams) error {
	s.app.ApiKeyStatus = arg.ApiKeyStatus
	s.app.NewapiKeyID = arg.NewapiKeyID
	s.app.NewapiKeyCiphertext = arg.NewapiKeyCiphertext
	return nil
}

// spec-A2b：SetAppContainer 已从 RuntimeOperationStore 接口移除，stub 不再实现。

// fakeNotFound 模拟记录不存在时 store 返回的错误；使用 sql.ErrNoRows 以匹配
// service 层 errors.Is(err, sql.ErrNoRows) 的映射逻辑，确保 ErrNotFound 正确触发。
var fakeNotFound = sql.ErrNoRows

type fakeNotifier struct {
	lastJobID string
	err       error
}

func (f *fakeNotifier) Enqueue(_ context.Context, jobID string) error {
	f.lastJobID = jobID
	return f.err
}

// TestTrigger_DisabledPrincipal_Denied 验证触发禁用PrincipalDenied的预期行为场景。
func TestTrigger_DisabledPrincipal_Denied(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.userStatus = domain.StatusDisabled
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	_, err := svc.Trigger(context.Background(), platformAdmin(), testRuntimeOpAppID, RuntimeOperationStart)
	require.ErrorIs(t, err, ErrRuntimeOperationDenied)
}

// TestRuntimeOperationTriggerDeleteEmitsCascadeDetail 验证 delete 操作审计 metadata 包含级联渠道绑定数。
// 审计迁移后不再写入冻结中文文案，改用 metadata.cascade_count 存储供前端按语言渲染。
func TestRuntimeOperationTriggerDeleteEmitsCascadeDetail(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.channelBindingCount = 2
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	_, err := svc.Trigger(context.Background(), runtimeOrgAdminPrincipal(), testRuntimeOpAppID, RuntimeOperationDelete)
	require.NoError(t, err)
	// delete 操作的审计 metadata 必须包含级联渠道绑定数（cascade_count），供前端渲染详情。
	require.NotEmpty(t, store.lastAuditMeta, "delete 操作应写入 metadata")
	var meta map[string]any
	require.NoError(t, json.Unmarshal(store.lastAuditMeta, &meta))
	require.Equal(t, float64(2), meta["cascade_count"], "metadata.cascade_count 应为渠道绑定数")
}

// TestRuntimeOperationTriggerStartHasNoDetail 验证非 delete 操作（如 start）的审计 metadata 为空。
// 非 delete 操作无需额外 metadata，action 字段本身已描述操作类型。
func TestRuntimeOperationTriggerStartHasNoDetail(t *testing.T) {
	store := newRuntimeOperationStub(t)
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	_, err := svc.Trigger(context.Background(), runtimeOrgAdminPrincipal(), testRuntimeOpAppID, RuntimeOperationStart)
	require.NoError(t, err)
	// 非 delete op 不需要 metadata，MetadataJson 应为空。
	require.Empty(t, store.lastAuditMeta, "非 delete 操作的 metadata 应为空")
}

// TestRequestInitialize_DisabledPrincipal_Denied 验证请求初始化禁用PrincipalDenied的预期行为场景。
func TestRequestInitialize_DisabledPrincipal_Denied(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.Status = domain.AppStatusError
	store.userStatus = domain.StatusDisabled
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	_, err := svc.RequestInitialize(context.Background(), platformAdmin(), testRuntimeOpAppID)
	require.ErrorIs(t, err, ErrRuntimeOperationDenied)
}
