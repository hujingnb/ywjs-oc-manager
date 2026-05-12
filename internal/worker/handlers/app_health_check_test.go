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

func (s *fakeHealthStore) SetAppHealthState(_ context.Context, p sqlc.SetAppHealthStateParams) (sqlc.App, error) {
	s.healthState = p.HealthStateJson
	return s.app, nil
}

func (s *fakeHealthStore) CreateJob(_ context.Context, p sqlc.CreateJobParams) (sqlc.Job, error) {
	s.jobs = append(s.jobs, p)
	return sqlc.Job{ID: pgtype.UUID{Valid: true}}, nil
}

type fakeExecutor struct {
	result runtime.ExecResult
	err    error
}

func (e *fakeExecutor) ContainerExec(_ context.Context, _, _ string, _ []string) (runtime.ExecResult, error) {
	return e.result, e.err
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

// TestAppHealthCheckSuccessClearsError 验证应用健康检查Check成功清空错误的成功路径场景。
func TestAppHealthCheckSuccessClearsError(t *testing.T) {
	app := makeAppForHealth(t)
	app.HealthStateJson = []byte(`{"last_error":"old"}`)
	store := &fakeHealthStore{app: app}
	exec := &fakeExecutor{result: runtime.ExecResult{ExitCode: 0, Stdout: `{"ok":true}`}}
	h := NewAppHealthCheckHandler(store, exec)
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

// TestAppHealthCheckFailureRecordsFailureWithoutRestart 验证应用健康检查Check失败记录失败不使用重启的错误映射或错误记录场景。
func TestAppHealthCheckFailureRecordsFailureWithoutRestart(t *testing.T) {
	store := &fakeHealthStore{app: makeAppForHealth(t)}
	exec := &fakeExecutor{result: runtime.ExecResult{ExitCode: 1, Stdout: "Connection refused"}}
	h := NewAppHealthCheckHandler(store, exec)
	job := sqlc.Job{Type: domain.JobTypeAppHealthCheck, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	err := h.Handle(context.Background(), job)
	require.NoError(t, err)
	require.Equal(t, 0, len(store.jobs))
	var state healthState
	require.NoError(t, json.Unmarshal(store.healthState, &state))
	require.Equal(t, "exit=1 Connection refused", state.LastError)
	require.Equal(t, 1, len(state.Failures))
	require.Equal(t, 0, len(state.RestartedAt))
}

// TestAppHealthCheckExhaustedBudgetSetsError 验证应用健康检查Check耗尽预算并设置错误的预期行为场景。
func TestAppHealthCheckExhaustedBudgetSetsError(t *testing.T) {
	app := makeAppForHealth(t)
	// 已经累积 max_per_window=2 次失败，再失败一次 → 触发 error 状态。
	now := time.Now()
	prior := []time.Time{now.Add(-30 * time.Second), now.Add(-10 * time.Second)}
	stateBytes, _ := json.Marshal(healthState{Failures: prior, RestartedAt: prior})
	app.HealthStateJson = stateBytes
	store := &fakeHealthStore{app: app}
	exec := &fakeExecutor{result: runtime.ExecResult{ExitCode: 1, Stdout: "fail"}}
	h := NewAppHealthCheckHandler(store, exec)
	h.now = func() time.Time { return now.Add(time.Second) }
	job := sqlc.Job{Type: domain.JobTypeAppHealthCheck, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	err := h.Handle(context.Background(), job)
	require.NoError(t, err)
	if len(store.statusUpdates) != 1 || store.statusUpdates[0] != domain.AppStatusError {
		t.Fatalf("status 应被推到 error; updates=%v", store.statusUpdates)
	}
	require.Equal(t, 0, len(store.jobs))
}

// TestAppHealthCheckExecErrorAlsoTreatedAsFailure 验证应用健康检查Check执行错误也视为作为失败的预期行为场景。
func TestAppHealthCheckExecErrorAlsoTreatedAsFailure(t *testing.T) {
	store := &fakeHealthStore{app: makeAppForHealth(t)}
	exec := &fakeExecutor{err: errors.New("docker dial")}
	h := NewAppHealthCheckHandler(store, exec)
	job := sqlc.Job{Type: domain.JobTypeAppHealthCheck, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	err := h.Handle(context.Background(), job)
	require.NoError(t, err)
	require.Equal(t, 0, len(store.jobs))
}

// TestAppHealthCheckSanitizesNULInFailureText 验证应用健康检查Check清理NULIn失败Text的预期行为场景。
func TestAppHealthCheckSanitizesNULInFailureText(t *testing.T) {
	store := &fakeHealthStore{app: makeAppForHealth(t)}
	exec := &fakeExecutor{result: runtime.ExecResult{ExitCode: 1, Stdout: "bad\x00json"}}
	h := NewAppHealthCheckHandler(store, exec)
	job := sqlc.Job{Type: domain.JobTypeAppHealthCheck, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	err := h.Handle(context.Background(), job)
	require.NoError(t, err)
	if strings.Contains(string(store.healthState), `\u0000`) {
		t.Fatalf("health_state_json 不应包含 PostgreSQL JSONB 拒绝的 NUL 转义: %s", store.healthState)
	}
	var state healthState
	require.NoError(t, json.Unmarshal(store.healthState, &state))
	require.Equal(t, "exit=1 bad�json", state.LastError)
}

// TestAppHealthCheckNoneModeSkipsRestart 验证应用健康检查Check无模式跳过重启的特殊分支或幂等场景。
func TestAppHealthCheckNoneModeSkipsRestart(t *testing.T) {
	app := makeAppForHealth(t)
	app.RestartPolicyJson = []byte(`{"mode":"none","max_per_window":5,"window_seconds":600}`)
	store := &fakeHealthStore{app: app}
	exec := &fakeExecutor{result: runtime.ExecResult{ExitCode: 1, Stdout: "fail"}}
	h := NewAppHealthCheckHandler(store, exec)
	job := sqlc.Job{Type: domain.JobTypeAppHealthCheck, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	err := h.Handle(context.Background(), job)
	require.NoError(t, err)
	require.Equal(t, 0, len(store.jobs))
	require.Equal(t, 0, len(store.statusUpdates))
}
