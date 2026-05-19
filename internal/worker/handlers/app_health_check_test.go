package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stretchr/testify/require"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/runtime"
	"oc-manager/internal/store/sqlc"
)

type fakeHealthStore struct {
	app           sqlc.App
	statusUpdates []string
	healthState   []byte
	jobs          []sqlc.CreateJobParams
}

func (s *fakeHealthStore) GetApp(_ context.Context, _ pgtype.UUID) (sqlc.App, error) {
	return s.app, nil
}

func (s *fakeHealthStore) SetAppStatus(_ context.Context, p sqlc.SetAppStatusParams) (sqlc.App, error) {
	s.statusUpdates = append(s.statusUpdates, p.Status)
	s.app.Status = p.Status
	return s.app, nil
}

func (s *fakeHealthStore) SoftDeleteApp(_ context.Context, _ pgtype.UUID) (sqlc.App, error) {
	return s.app, nil
}

// SetAppModelSynced 实现 AppRuntimeStore 接口；健康检查流程不触发模型同步，此处仅满足接口约束。
func (s *fakeHealthStore) SetAppModelSynced(_ context.Context, _ pgtype.UUID) (sqlc.App, error) {
	return s.app, nil
}

func (s *fakeHealthStore) SetAppHealthState(_ context.Context, p sqlc.SetAppHealthStateParams) (sqlc.App, error) {
	s.healthState = p.HealthStateJson
	return s.app, nil
}

func (s *fakeHealthStore) CreateJob(_ context.Context, p sqlc.CreateJobParams) (sqlc.Job, error) {
	s.jobs = append(s.jobs, p)
	return sqlc.Job{ID: pgtype.UUID{Valid: true}}, nil
}

// fakeInspector 实现 ContainerInspector，返回预设的 docker inspect 结果。
// Hermes 时代健康检查依赖 InspectContainer 读取 Health.Status，不再 exec curl /healthz。
type fakeInspector struct {
	info runtime.ContainerInfo
	err  error
}

func (f *fakeInspector) InspectContainer(_ context.Context, _, _ string) (runtime.ContainerInfo, error) {
	return f.info, f.err
}

type capturingNotifier struct {
	count int
}

func (n *capturingNotifier) Enqueue(_ context.Context, _ string) error {
	n.count++
	return nil
}

func makeAppForHealth(t *testing.T) sqlc.App {
	t.Helper()
	app := makeAppForRefresh(t)
	app.Status = domain.AppStatusRunning
	app.RestartPolicyJson = []byte(`{"mode":"on_failure","max_per_window":2,"window_seconds":600}`)
	return app
}

// TestAppHealthCheckSuccessClearsError 验证容器 HEALTHCHECK 报 healthy 时
// handler 清除 last_error 并写 last_success_at 的成功路径场景。
func TestAppHealthCheckSuccessClearsError(t *testing.T) {
	app := makeAppForHealth(t)
	app.HealthStateJson = []byte(`{"last_error":"old"}`)
	store := &fakeHealthStore{app: app}
	// 场景：docker inspect 返回 healthy，健康检查应当清除错误状态。
	inspector := &fakeInspector{info: runtime.ContainerInfo{Health: runtime.ContainerHealth{Status: "healthy"}}}
	h := NewAppHealthCheckHandler(store, inspector)
	job := sqlc.Job{Type: domain.JobTypeAppHealthCheck, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	err := h.Handle(context.Background(), job)
	require.NoError(t, err)
	var state healthState
	_ = json.Unmarshal(store.healthState, &state)
	require.Equal(t, "", state.LastError)
	if state.LastSuccessAt.IsZero() {
		t.Fatalf("last_success_at 未写入")
	}
}

// TestAppHealthCheckFailureRecordsFailureWithoutRestart 验证容器 HEALTHCHECK 报 unhealthy 时
// handler 记录失败但未超出 restart budget 时不触发 error 状态的错误记录场景。
func TestAppHealthCheckFailureRecordsFailureWithoutRestart(t *testing.T) {
	store := &fakeHealthStore{app: makeAppForHealth(t)}
	// 场景：docker inspect 返回 unhealthy，应记录失败次数但不推 error 状态（budget 未耗尽）。
	inspector := &fakeInspector{info: runtime.ContainerInfo{Health: runtime.ContainerHealth{Status: "unhealthy", Output: "connection refused"}}}
	h := NewAppHealthCheckHandler(store, inspector)
	job := sqlc.Job{Type: domain.JobTypeAppHealthCheck, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	err := h.Handle(context.Background(), job)
	require.NoError(t, err)
	require.Equal(t, 0, len(store.jobs))
	var state healthState
	require.NoError(t, json.Unmarshal(store.healthState, &state))
	require.Contains(t, state.LastError, "unhealthy")
	require.Equal(t, 1, len(state.Failures))
	require.Equal(t, 0, len(state.RestartedAt))
}

// TestAppHealthCheckExhaustedBudgetSetsError 验证失败次数超出 restart budget 时
// handler 把 apps.status 推到 error 的错误熔断场景。
func TestAppHealthCheckExhaustedBudgetSetsError(t *testing.T) {
	app := makeAppForHealth(t)
	// 已经累积 max_per_window=2 次失败，再失败一次 → 触发 error 状态。
	now := time.Now()
	prior := []time.Time{now.Add(-30 * time.Second), now.Add(-10 * time.Second)}
	stateBytes, _ := json.Marshal(healthState{Failures: prior, RestartedAt: prior})
	app.HealthStateJson = stateBytes
	store := &fakeHealthStore{app: app}
	// 场景：连续失败超出 budget，应推 error 状态并停止重试。
	inspector := &fakeInspector{info: runtime.ContainerInfo{Health: runtime.ContainerHealth{Status: "unhealthy"}}}
	h := NewAppHealthCheckHandler(store, inspector)
	h.now = func() time.Time { return now.Add(time.Second) }
	job := sqlc.Job{Type: domain.JobTypeAppHealthCheck, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	err := h.Handle(context.Background(), job)
	require.NoError(t, err)
	if len(store.statusUpdates) != 1 || store.statusUpdates[0] != domain.AppStatusError {
		t.Fatalf("status 应被推到 error; updates=%v", store.statusUpdates)
	}
	require.Equal(t, 0, len(store.jobs))
}

// TestAppHealthCheckInspectErrorAlsoTreatedAsFailure 验证 InspectContainer 失败时
// handler 也记录为健康检查失败的错误场景。
func TestAppHealthCheckInspectErrorAlsoTreatedAsFailure(t *testing.T) {
	store := &fakeHealthStore{app: makeAppForHealth(t)}
	// 场景：docker inspect 本身失败（docker daemon 不可用等），应视为检查失败。
	inspector := &fakeInspector{err: errors.New("docker dial")}
	h := NewAppHealthCheckHandler(store, inspector)
	job := sqlc.Job{Type: domain.JobTypeAppHealthCheck, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	err := h.Handle(context.Background(), job)
	require.NoError(t, err)
	require.Equal(t, 0, len(store.jobs))
}

// TestAppHealthCheckSanitizesNULInFailureText 验证 health state 中的 NUL 字节被清洗
// 避免 PostgreSQL JSONB 报错的数据清洗场景。
func TestAppHealthCheckSanitizesNULInFailureText(t *testing.T) {
	store := &fakeHealthStore{app: makeAppForHealth(t)}
	// 场景：docker inspect 返回含 NUL 字节的 Output，写库前必须清洗。
	inspector := &fakeInspector{info: runtime.ContainerInfo{Health: runtime.ContainerHealth{Status: "unhealthy", Output: "bad\x00json"}}}
	h := NewAppHealthCheckHandler(store, inspector)
	job := sqlc.Job{Type: domain.JobTypeAppHealthCheck, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	err := h.Handle(context.Background(), job)
	require.NoError(t, err)
	if strings.Contains(string(store.healthState), "\u0000") {
		t.Fatalf("health_state_json 不应包含 PostgreSQL JSONB 拒绝的 NUL 转义: %s", store.healthState)
	}
	var state healthState
	require.NoError(t, json.Unmarshal(store.healthState, &state))
	require.Contains(t, state.LastError, "unhealthy")
}

// TestAppHealthCheck_ContainerStoppedTriggersAutoStart 验证 health check 发现容器
// Status != "running"(被基础设施事件意外停掉)时,在 restart budget 内主动调
// StartContainer 自愈,并把时间戳记入 health_state.restarted_at。
func TestAppHealthCheck_ContainerStoppedTriggersAutoStart(t *testing.T) {
	store := &fakeHealthStore{app: makeAppForHealth(t)}
	// docker inspect 返回 Status=exited(容器停了)。
	inspector := &fakeInspector{info: runtime.ContainerInfo{Status: "exited"}}
	lifecycle := &fakeLifecycle{}
	h := NewAppHealthCheckHandler(store, inspector)
	h.SetLifecycle(lifecycle)
	job := sqlc.Job{Type: domain.JobTypeAppHealthCheck, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	err := h.Handle(context.Background(), job)
	require.NoError(t, err)
	// StartContainer 被调一次,自愈成功。
	require.Equal(t, 1, lifecycle.startCalls)
	// app.status 不被推到 error(budget 还在);last_error 记录"container stopped"。
	require.Equal(t, 0, len(store.statusUpdates))
	var state healthState
	require.NoError(t, json.Unmarshal(store.healthState, &state))
	require.Contains(t, state.LastError, "container stopped")
	require.Equal(t, 1, len(state.RestartedAt), "应记一次自愈时间戳")
}

// TestAppHealthCheck_ContainerStoppedNoLifecycleSkipsAutoStart 验证未注入 lifecycle 时
// handler 退回到旧行为(只记失败,不自动拉起),保持向后兼容。
func TestAppHealthCheck_ContainerStoppedNoLifecycleSkipsAutoStart(t *testing.T) {
	store := &fakeHealthStore{app: makeAppForHealth(t)}
	inspector := &fakeInspector{info: runtime.ContainerInfo{Status: "exited"}}
	h := NewAppHealthCheckHandler(store, inspector)
	// 不调 SetLifecycle。
	job := sqlc.Job{Type: domain.JobTypeAppHealthCheck, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	err := h.Handle(context.Background(), job)
	require.NoError(t, err)
	var state healthState
	require.NoError(t, json.Unmarshal(store.healthState, &state))
	require.Contains(t, state.LastError, "container stopped")
	// 无 lifecycle 时不应产生 restarted_at 记录。
	require.Equal(t, 0, len(state.RestartedAt))
}

// TestAppHealthCheck_ContainerStoppedExhaustedBudgetSetsError 验证容器停了且 budget
// 已耗尽时,handler 不再自愈,而是把 status 推到 error 让用户干预。
func TestAppHealthCheck_ContainerStoppedExhaustedBudgetSetsError(t *testing.T) {
	app := makeAppForHealth(t)
	now := time.Now()
	prior := []time.Time{now.Add(-30 * time.Second), now.Add(-10 * time.Second)}
	stateBytes, _ := json.Marshal(healthState{Failures: prior})
	app.HealthStateJson = stateBytes
	store := &fakeHealthStore{app: app}
	inspector := &fakeInspector{info: runtime.ContainerInfo{Status: "exited"}}
	lifecycle := &fakeLifecycle{}
	h := NewAppHealthCheckHandler(store, inspector)
	h.SetLifecycle(lifecycle)
	h.now = func() time.Time { return now.Add(time.Second) }
	job := sqlc.Job{Type: domain.JobTypeAppHealthCheck, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	err := h.Handle(context.Background(), job)
	require.NoError(t, err)
	// budget 耗尽分支优先于自愈分支:不再 StartContainer,直接 status=error。
	require.Equal(t, 0, lifecycle.startCalls)
	require.Equal(t, []string{domain.AppStatusError}, store.statusUpdates)
}

// TestAppHealthCheckNoneModeSkipsRestart 验证 restart_policy.mode=none 时
// 即使失败次数超出 budget 也不推 error 状态的特殊场景。
func TestAppHealthCheckNoneModeSkipsRestart(t *testing.T) {
	app := makeAppForHealth(t)
	app.RestartPolicyJson = []byte(`{"mode":"none","max_per_window":5,"window_seconds":600}`)
	store := &fakeHealthStore{app: app}
	// 场景：mode=none 时 exhaustedRestartBudget 总返回 false，不推 error。
	inspector := &fakeInspector{info: runtime.ContainerInfo{Health: runtime.ContainerHealth{Status: "unhealthy"}}}
	h := NewAppHealthCheckHandler(store, inspector)
	job := sqlc.Job{Type: domain.JobTypeAppHealthCheck, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	err := h.Handle(context.Background(), job)
	require.NoError(t, err)
	require.Equal(t, 0, len(store.jobs))
	require.Equal(t, 0, len(store.statusUpdates))
}
