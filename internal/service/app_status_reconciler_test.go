// Package service 的 app_status_reconciler_test 覆盖 pod 状态同步的各状态映射与守卫边界。
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/k8sorch"
	"oc-manager/internal/store/sqlc"
)

// ============================================================
// fake appStatusStore
// ============================================================

// fakeAppStatusStore 是 appStatusStore 的内存 fake，记录所有调用供断言使用。
type fakeAppStatusStore struct {
	// rows 是 ListRunningApps 返回的 app id 列表（spec-A2b：只含 id）。
	rows []string
	// apps 按 ID 存储 GetApp 返回的 app 数据。
	apps map[string]sqlc.App

	// snapshotCalls 记录每次 SetAppRuntimeSnapshot 的入参。
	snapshotCalls []sqlc.SetAppRuntimeSnapshotParams
	// statusCalls 记录每次 SetAppStatus 的入参。
	statusCalls []sqlc.SetAppStatusParams

	// listErr 若非 nil，ListRunningApps 返回该错误（模拟整轮失败）。
	listErr error
	// snapshotErr 若非 nil，SetAppRuntimeSnapshot 返回该错误（模拟 DB 抖动写快照失败）。
	snapshotErr error
}

func newFakeAppStatusStore() *fakeAppStatusStore {
	return &fakeAppStatusStore{
		apps: map[string]sqlc.App{},
	}
}

// ListRunningApps 返回 []string（spec-A2b：只含 id，不含节点/容器字段）。
func (f *fakeAppStatusStore) ListRunningApps(_ context.Context) ([]string, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.rows, nil
}

func (f *fakeAppStatusStore) GetApp(_ context.Context, id string) (sqlc.App, error) {
	app, ok := f.apps[id]
	if !ok {
		return sqlc.App{}, errors.New("not found")
	}
	return app, nil
}

func (f *fakeAppStatusStore) SetAppStatus(_ context.Context, arg sqlc.SetAppStatusParams) error {
	f.statusCalls = append(f.statusCalls, arg)
	return nil
}

func (f *fakeAppStatusStore) SetAppRuntimeSnapshot(_ context.Context, arg sqlc.SetAppRuntimeSnapshotParams) error {
	f.snapshotCalls = append(f.snapshotCalls, arg)
	// 若设置了 snapshotErr，模拟 DB 抖动写快照失败。
	return f.snapshotErr
}

// ============================================================
// fake Orchestrator
// ============================================================

// fakeOrch 是 k8sorch.Orchestrator 的 fake，按 appID 返回预设的 AppStatus 或错误。
type fakeOrch struct {
	// results 按 appID 存储预设结果（AppStatus, error）。
	results map[string]fakeOrchResult
}

type fakeOrchResult struct {
	status k8sorch.AppStatus
	err    error
}

func newFakeOrch() *fakeOrch {
	return &fakeOrch{results: map[string]fakeOrchResult{}}
}

// set 设置指定 appID 的 orch.Status 返回值。
func (f *fakeOrch) set(appID string, st k8sorch.AppStatus, err error) {
	f.results[appID] = fakeOrchResult{status: st, err: err}
}

func (f *fakeOrch) Status(_ context.Context, appID string) (k8sorch.AppStatus, error) {
	r, ok := f.results[appID]
	if !ok {
		return k8sorch.AppStatus{}, errors.New("no stub for " + appID)
	}
	return r.status, r.err
}

// 以下方法仅为实现 k8sorch.Orchestrator 接口，reconciler 测试不会调用。
func (f *fakeOrch) EnsureApp(_ context.Context, _ k8sorch.AppSpec) error             { panic("not used") }
func (f *fakeOrch) WaitReady(_ context.Context, _ string, _ time.Duration) error     { panic("not used") }
func (f *fakeOrch) Scale(_ context.Context, _ string, _ int32) error                 { panic("not used") }
func (f *fakeOrch) UpdateImage(_ context.Context, _, _ string) error                 { panic("not used") }
func (f *fakeOrch) Delete(_ context.Context, _ string) error                         { panic("not used") }
// RolloutRestart 在 reconciler 测试中不被调用，stub 防御性 panic（与其它未用方法一致）。
func (f *fakeOrch) RolloutRestart(_ context.Context, _ string) error                 { panic("not used") }

// ============================================================
// 辅助：构造常用 app id 字符串和 App
// ============================================================

// appWithStatus 构造 GetApp 返回的 App，只设置 ID 和 Status。
func appWithStatus(id, status string) sqlc.App {
	return sqlc.App{ID: id, Status: status}
}

// ============================================================
// 单元测试
// ============================================================

const (
	// 测试用 app ID，使用 UUID 格式保持一致性。
	appID1 = "00000000-0000-0000-0001-000000000001"
	appID2 = "00000000-0000-0000-0001-000000000002"
)

// TestAppStatusReconciler_RunningReadyPod 验证：running app + pod Ready（Phase Running, Ready true）
// → 写 snapshot、不调 SetAppStatus（正常运行态，无需变更 DB status）。
func TestAppStatusReconciler_RunningReadyPod(t *testing.T) {
	store := newFakeAppStatusStore()
	// spec-A2b：ListRunningApps 返回 []string（只含 app id）。
	store.rows = []string{appID1}
	store.apps[appID1] = appWithStatus(appID1, domain.AppStatusRunning)

	orch := newFakeOrch()
	// pod Running 且 Ready，携带 Raw 快照数据。
	orch.set(appID1, k8sorch.AppStatus{
		Phase:   "Running",
		Ready:   true,
		Raw:     []byte(`{"phase":"Running"}`),
		Message: "",
	}, nil)

	rec := NewAppStatusReconciler(store, orch)
	require.NoError(t, rec.Tick(context.Background()))

	// 写了 snapshot。
	assert.Len(t, store.snapshotCalls, 1, "应写一次 snapshot")
	assert.Equal(t, appID1, store.snapshotCalls[0].ID)

	// 没有 SetAppStatus 调用，pod 正常不应变更 status。
	assert.Empty(t, store.statusCalls, "pod Ready 不应触发 SetAppStatus")
}

// TestAppStatusReconciler_RunningNotFoundPod 验证：running app + Phase NotFound
// → 写 snapshot + SetAppStatus(error)（pod 被带外删除，确定性坏态）。
func TestAppStatusReconciler_RunningNotFoundPod(t *testing.T) {
	store := newFakeAppStatusStore()
	// spec-A2b：ListRunningApps 返回 []string（只含 app id）。
	store.rows = []string{appID1}
	store.apps[appID1] = appWithStatus(appID1, domain.AppStatusRunning)

	orch := newFakeOrch()
	// Pod 已消失，Phase=NotFound，携带原始快照。
	orch.set(appID1, k8sorch.AppStatus{
		Phase:   "NotFound",
		Ready:   false,
		Raw:     []byte(`{"phase":"NotFound"}`),
		Message: "",
	}, nil)

	rec := NewAppStatusReconciler(store, orch)
	require.NoError(t, rec.Tick(context.Background()))

	// 写了 snapshot（观测记录）。
	assert.Len(t, store.snapshotCalls, 1, "应写一次 snapshot")

	// 推到 error。
	require.Len(t, store.statusCalls, 1, "应调用一次 SetAppStatus")
	assert.Equal(t, domain.AppStatusError, store.statusCalls[0].Status, "status 应为 error")
	assert.Equal(t, appID1, store.statusCalls[0].ID, "ID 应匹配")
}

// TestAppStatusReconciler_RunningCrashLoopBackOff 验证：running app + Message 含 CrashLoopBackOff
// → 写 snapshot + SetAppStatus(error)（容器反复崩溃，确定性坏态）。
func TestAppStatusReconciler_RunningCrashLoopBackOff(t *testing.T) {
	store := newFakeAppStatusStore()
	// spec-A2b：ListRunningApps 返回 []string（只含 app id）。
	store.rows = []string{appID1}
	store.apps[appID1] = appWithStatus(appID1, domain.AppStatusRunning)

	orch := newFakeOrch()
	// 容器 CrashLoopBackOff，Phase 可能还是 Running 但 Message 携带故障信息。
	orch.set(appID1, k8sorch.AppStatus{
		Phase:   "Running",
		Ready:   false,
		Raw:     []byte(`{"phase":"Running"}`),
		Message: "Back-off restarting failed container: CrashLoopBackOff",
	}, nil)

	rec := NewAppStatusReconciler(store, orch)
	require.NoError(t, rec.Tick(context.Background()))

	// 写了 snapshot。
	assert.Len(t, store.snapshotCalls, 1)

	// 推到 error。
	require.Len(t, store.statusCalls, 1, "CrashLoopBackOff 应推 error")
	assert.Equal(t, domain.AppStatusError, store.statusCalls[0].Status)
	assert.Equal(t, appID1, store.statusCalls[0].ID)
}

// TestAppStatusReconciler_RunningFailedPod 验证：running app + Phase Failed
// → 写 snapshot + SetAppStatus(error)（pod 已进入 Failed 相位，确定性失败）。
func TestAppStatusReconciler_RunningFailedPod(t *testing.T) {
	store := newFakeAppStatusStore()
	// spec-A2b：ListRunningApps 返回 []string（只含 app id）。
	store.rows = []string{appID1}
	store.apps[appID1] = appWithStatus(appID1, domain.AppStatusRunning)

	orch := newFakeOrch()
	// Pod 进入 Failed 相位（如 OOMKilled restartPolicy=Never）。
	orch.set(appID1, k8sorch.AppStatus{
		Phase:   "Failed",
		Ready:   false,
		Raw:     []byte(`{"phase":"Failed"}`),
		Message: "OOMKilled",
	}, nil)

	rec := NewAppStatusReconciler(store, orch)
	require.NoError(t, rec.Tick(context.Background()))

	// 写了 snapshot。
	assert.Len(t, store.snapshotCalls, 1)

	// 推到 error。
	require.Len(t, store.statusCalls, 1, "Phase Failed 应推 error")
	assert.Equal(t, domain.AppStatusError, store.statusCalls[0].Status)
	assert.Equal(t, appID1, store.statusCalls[0].ID)
}

// TestAppStatusReconciler_RunningPendingPod 验证：running app + Phase Pending（瞬态，未 Ready）
// → 只写 snapshot，不推 error（Pending 属于正常启动/重启流程中的瞬态，Deployment 自管）。
func TestAppStatusReconciler_RunningPendingPod(t *testing.T) {
	store := newFakeAppStatusStore()
	// spec-A2b：ListRunningApps 返回 []string（只含 app id）。
	store.rows = []string{appID1}
	store.apps[appID1] = appWithStatus(appID1, domain.AppStatusRunning)

	orch := newFakeOrch()
	// Pod 正在调度/拉镜像，瞬态 Pending。
	orch.set(appID1, k8sorch.AppStatus{
		Phase:   "Pending",
		Ready:   false,
		Raw:     []byte(`{"phase":"Pending"}`),
		Message: "",
	}, nil)

	rec := NewAppStatusReconciler(store, orch)
	require.NoError(t, rec.Tick(context.Background()))

	// 写了 snapshot（观测记录）。
	assert.Len(t, store.snapshotCalls, 1, "应写一次 snapshot")

	// Pending 是瞬态，不应推 error。
	assert.Empty(t, store.statusCalls, "Pending 瞬态不应触发 SetAppStatus")
}

// TestAppStatusReconciler_BindingWaitingNotFound 验证：binding_waiting app + pod NotFound
// → 只写 snapshot，绝不调用 SetAppStatus（守卫生效，不越权 error binding_waiting app）。
// binding_waiting → running 由渠道绑定流程负责，reconciler 不越权迁移任何非 running 状态。
func TestAppStatusReconciler_BindingWaitingNotFound(t *testing.T) {
	store := newFakeAppStatusStore()
	// spec-A2b：ListRunningApps 返回 []string（只含 app id）。
	store.rows = []string{appID1}
	// GetApp 返回 binding_waiting，模拟渠道正在登录中的 app。
	store.apps[appID1] = appWithStatus(appID1, domain.AppStatusBindingWaiting)

	orch := newFakeOrch()
	// Pod 已消失，但 app 处于 binding_waiting，守卫应阻止推 error。
	orch.set(appID1, k8sorch.AppStatus{
		Phase:   "NotFound",
		Ready:   false,
		Raw:     []byte(`{"phase":"NotFound"}`),
		Message: "",
	}, nil)

	rec := NewAppStatusReconciler(store, orch)
	require.NoError(t, rec.Tick(context.Background()))

	// 写了 snapshot（观测记录依然写入）。
	assert.Len(t, store.snapshotCalls, 1, "应写一次 snapshot")

	// 守卫生效：binding_waiting 不应被推到 error。
	assert.Empty(t, store.statusCalls, "binding_waiting app 不应触发 SetAppStatus，守卫应阻止越权迁移")
}

// TestAppStatusReconciler_OrchErrorSkipsApp 验证：orch.Status 返回错误时，
// 该 app 被跳过（不写 snapshot 不改状态），同轮其他正常 app 依然被处理。
// 构造两个 app：appID1 orch 返回 error，appID2 正常 NotFound → error。
func TestAppStatusReconciler_OrchErrorSkipsApp(t *testing.T) {
	store := newFakeAppStatusStore()
	// spec-A2b：ListRunningApps 返回 []string（只含 app id）。
	store.rows = []string{
		appID1, // orch 会返回错误，应跳过
		appID2, // orch 正常，pod NotFound → 推 error
	}
	store.apps[appID1] = appWithStatus(appID1, domain.AppStatusRunning)
	store.apps[appID2] = appWithStatus(appID2, domain.AppStatusRunning)

	orch := newFakeOrch()
	// appID1 的 orch.Status 返回错误，模拟 k8s API 不可达。
	orch.set(appID1, k8sorch.AppStatus{}, errors.New("k8s api error"))
	// appID2 正常返回 NotFound，应被推 error。
	orch.set(appID2, k8sorch.AppStatus{
		Phase: "NotFound",
		Raw:   []byte(`{"phase":"NotFound"}`),
	}, nil)

	rec := NewAppStatusReconciler(store, orch)
	// 整轮应返回 nil（尽力而为，不因单个 app 失败阻断整轮）。
	require.NoError(t, rec.Tick(context.Background()))

	// appID1 orch 错误，不应有任何写入。
	for _, call := range store.snapshotCalls {
		assert.NotEqual(t, appID1, call.ID, "appID1 orch 出错，不应写 snapshot")
	}
	for _, call := range store.statusCalls {
		assert.NotEqual(t, appID1, call.ID, "appID1 orch 出错，不应写 status")
	}

	// appID2 正常处理，应推 error。
	require.Len(t, store.statusCalls, 1, "appID2 应被正常处理并推 error")
	assert.Equal(t, domain.AppStatusError, store.statusCalls[0].Status)
	assert.Equal(t, appID2, store.statusCalls[0].ID)

	// appID2 的 snapshot 也应被写入。
	require.Len(t, store.snapshotCalls, 1, "appID2 应写 snapshot")
	assert.Equal(t, appID2, store.snapshotCalls[0].ID)
}

// TestAppStatusReconciler_EmptyRawSkipsSnapshot 验证：st.Raw 为空时不调用 SetAppRuntimeSnapshot。
// 若快照为空（如 NotFound 时可能无 pod 数据），跳过 snapshot 写入；状态守卫仍正常执行。
func TestAppStatusReconciler_EmptyRawSkipsSnapshot(t *testing.T) {
	store := newFakeAppStatusStore()
	// spec-A2b：ListRunningApps 返回 []string（只含 app id）。
	store.rows = []string{appID1}
	store.apps[appID1] = appWithStatus(appID1, domain.AppStatusRunning)

	orch := newFakeOrch()
	// st.Raw 为空，Phase Running+Ready，正常运行。
	orch.set(appID1, k8sorch.AppStatus{
		Phase: "Running",
		Ready: true,
		Raw:   nil, // 空 Raw，不应写 snapshot
	}, nil)

	rec := NewAppStatusReconciler(store, orch)
	require.NoError(t, rec.Tick(context.Background()))

	// Raw 为空，不应调用 SetAppRuntimeSnapshot。
	assert.Empty(t, store.snapshotCalls, "Raw 为空时不应调用 SetAppRuntimeSnapshot")
	// pod Ready，不应改 status。
	assert.Empty(t, store.statusCalls)
}

// TestAppStatusReconciler_SnapshotWriteErrorDoesNotBlockGuard 验证：
// running app + pod NotFound + SetAppRuntimeSnapshot 返回错误时，
// running→error 状态守卫仍正常触发（DB 抖动写快照失败不阻塞错误状态同步）。
// 这是 Critical 修复的直接覆盖：确保快照写失败不再 continue 跳过守卫逻辑。
func TestAppStatusReconciler_SnapshotWriteErrorDoesNotBlockGuard(t *testing.T) {
	store := newFakeAppStatusStore()
	// spec-A2b：ListRunningApps 返回 []string（只含 app id）。
	store.rows = []string{appID1}
	store.apps[appID1] = appWithStatus(appID1, domain.AppStatusRunning)
	// 模拟 DB 抖动：SetAppRuntimeSnapshot 返回错误。
	store.snapshotErr = errors.New("db write error: connection reset")

	orch := newFakeOrch()
	// pod NotFound（确定性坏态），携带 Raw 数据触发快照写入路径。
	orch.set(appID1, k8sorch.AppStatus{
		Phase:   "NotFound",
		Ready:   false,
		Raw:     []byte(`{"phase":"NotFound"}`),
		Message: "",
	}, nil)

	rec := NewAppStatusReconciler(store, orch)
	require.NoError(t, rec.Tick(context.Background()))

	// 快照写入被尝试了（snapshotCalls 有记录，只是返回了错误）。
	assert.Len(t, store.snapshotCalls, 1, "应尝试写一次 snapshot（即使失败）")

	// 关键断言：快照写失败不应拦住 running→error 守卫，SetAppStatus 必须被调用。
	// 若仍 continue 跳过守卫，此断言将失败，暴露 DB 抖动延迟错误暴露的 bug。
	require.Len(t, store.statusCalls, 1, "snapshot 写失败不应阻塞 running→error 状态守卫")
	assert.Equal(t, domain.AppStatusError, store.statusCalls[0].Status, "status 应被推到 error")
	assert.Equal(t, appID1, store.statusCalls[0].ID, "ID 应匹配")
}

// TestAppStatusReconciler_ListRunningAppsError 验证：ListRunningApps 返回错误时，
// Tick 立即返回错误（整轮失败，不继续处理）。
func TestAppStatusReconciler_ListRunningAppsError(t *testing.T) {
	store := newFakeAppStatusStore()
	// 模拟数据库整轮不可用。
	store.listErr = errors.New("db unavailable")

	orch := newFakeOrch()

	rec := NewAppStatusReconciler(store, orch)
	err := rec.Tick(context.Background())

	// 整轮失败应返回错误，让调度器记录并等待下轮重试。
	require.Error(t, err, "ListRunningApps 失败应返回错误")
	assert.Empty(t, store.snapshotCalls, "整轮失败不应写 snapshot")
	assert.Empty(t, store.statusCalls, "整轮失败不应写 status")
}

// ============================================================
// podIsBad 单元测试（直接测函数，覆盖各判定边界）
// ============================================================

// TestPodIsBad_Variants 以 table-driven 方式覆盖 podIsBad 的所有判定分支。
func TestPodIsBad_Variants(t *testing.T) {
	cases := []struct {
		st     k8sorch.AppStatus
		want   bool
		reason string // 中文说明覆盖的场景/边界/期望
	}{
		{
			// Phase NotFound：pod 被带外删除，确定性坏态。
			st:     k8sorch.AppStatus{Phase: "NotFound"},
			want:   true,
			reason: "NotFound 是确定性坏态，应返回 true",
		},
		{
			// Phase Failed：pod 进入终态 Failed，确定性坏态。
			st:     k8sorch.AppStatus{Phase: "Failed"},
			want:   true,
			reason: "Failed 是确定性坏态，应返回 true",
		},
		{
			// Message 含 CrashLoopBackOff：容器反复崩溃，业务可见持续异常。
			st:     k8sorch.AppStatus{Phase: "Running", Message: "CrashLoopBackOff"},
			want:   true,
			reason: "Message 含 CrashLoopBackOff 是确定性坏态，应返回 true",
		},
		{
			// Message 含更长文本，CrashLoopBackOff 嵌套在其中（strings.Contains 语义）。
			st:     k8sorch.AppStatus{Phase: "Running", Message: "Back-off restarting failed: CrashLoopBackOff (x3)"},
			want:   true,
			reason: "Message 嵌套 CrashLoopBackOff 仍应识别为坏态",
		},
		{
			// Phase Running + Ready true：pod 正常运行，应返回 false。
			st:     k8sorch.AppStatus{Phase: "Running", Ready: true},
			want:   false,
			reason: "Running+Ready 是正常态，应返回 false",
		},
		{
			// Phase Pending：调度/拉镜像瞬态，Deployment 自管，应返回 false。
			st:     k8sorch.AppStatus{Phase: "Pending"},
			want:   false,
			reason: "Pending 是瞬态，应返回 false",
		},
		{
			// Phase Running + Ready false：刚重启或健康检查未过，短暂抖动瞬态，应返回 false。
			st:     k8sorch.AppStatus{Phase: "Running", Ready: false},
			want:   false,
			reason: "Running 但未 Ready 是短暂抖动瞬态，应返回 false",
		},
		{
			// Phase Unknown：网络分区导致无法获取状态，保守不推 error，应返回 false。
			st:     k8sorch.AppStatus{Phase: "Unknown"},
			want:   false,
			reason: "Unknown 保守处理，应返回 false",
		},
		{
			// Message 为空且 Phase Running：无任何坏态信号，应返回 false。
			st:     k8sorch.AppStatus{Phase: "Running", Message: ""},
			want:   false,
			reason: "Message 为空、Phase Running，无坏态信号，应返回 false",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.reason, func(t *testing.T) {
			// 每条用例说明见 reason 字段，覆盖对应的 Phase/Message 判定边界。
			got := podIsBad(tc.st)
			assert.Equal(t, tc.want, got, tc.reason)
		})
	}
}
