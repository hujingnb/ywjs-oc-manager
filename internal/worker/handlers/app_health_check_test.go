package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

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

func TestAppHealthCheckSuccessClearsError(t *testing.T) {
	app := makeAppForHealth(t)
	app.HealthStateJson = []byte(`{"last_error":"old"}`)
	store := &fakeHealthStore{app: app}
	exec := &fakeExecutor{result: runtime.ExecResult{ExitCode: 0, Stdout: `{"ok":true}`}}
	h := NewAppHealthCheckHandler(store, exec, &capturingNotifier{})
	job := sqlc.Job{Type: domain.JobTypeAppHealthCheck, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	if err := h.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	var state healthState
	_ = json.Unmarshal(store.healthState, &state)
	if state.LastError != "" {
		t.Fatalf("last_error 应被清空; got %s", state.LastError)
	}
	if state.LastSuccessAt.IsZero() {
		t.Fatalf("last_success_at 未写入")
	}
}

func TestAppHealthCheckFailureTriggersRestart(t *testing.T) {
	store := &fakeHealthStore{app: makeAppForHealth(t)}
	exec := &fakeExecutor{result: runtime.ExecResult{ExitCode: 1, Stdout: "Connection refused"}}
	notifier := &capturingNotifier{}
	h := NewAppHealthCheckHandler(store, exec, notifier)
	job := sqlc.Job{Type: domain.JobTypeAppHealthCheck, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	if err := h.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(store.jobs) != 1 || store.jobs[0].Type != domain.JobTypeAppRestartContainer {
		t.Fatalf("应入队 1 条 app_restart_container; got %v", store.jobs)
	}
	if notifier.count != 1 {
		t.Fatalf("notifier 应触发一次; got %d", notifier.count)
	}
}

func TestAppHealthCheckExhaustedBudgetSetsError(t *testing.T) {
	app := makeAppForHealth(t)
	// 已经累积 max_per_window=2 次失败，再失败一次 → 触发 error 状态。
	now := time.Now()
	prior := []time.Time{now.Add(-30 * time.Second), now.Add(-10 * time.Second)}
	stateBytes, _ := json.Marshal(healthState{Failures: prior, RestartedAt: prior})
	app.HealthStateJson = stateBytes
	store := &fakeHealthStore{app: app}
	exec := &fakeExecutor{result: runtime.ExecResult{ExitCode: 1, Stdout: "fail"}}
	h := NewAppHealthCheckHandler(store, exec, &capturingNotifier{})
	h.now = func() time.Time { return now.Add(time.Second) }
	job := sqlc.Job{Type: domain.JobTypeAppHealthCheck, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	if err := h.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(store.statusUpdates) != 1 || store.statusUpdates[0] != domain.AppStatusError {
		t.Fatalf("status 应被推到 error; updates=%v", store.statusUpdates)
	}
	if len(store.jobs) != 0 {
		t.Fatalf("超额后不应入队 restart; got %v", store.jobs)
	}
}

func TestAppHealthCheckExecErrorAlsoTreatedAsFailure(t *testing.T) {
	store := &fakeHealthStore{app: makeAppForHealth(t)}
	exec := &fakeExecutor{err: errors.New("docker dial")}
	h := NewAppHealthCheckHandler(store, exec, &capturingNotifier{})
	job := sqlc.Job{Type: domain.JobTypeAppHealthCheck, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	if err := h.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(store.jobs) != 1 {
		t.Fatalf("exec 错误也应触发 restart 入队; got %v", store.jobs)
	}
}

func TestAppHealthCheckNoneModeSkipsRestart(t *testing.T) {
	app := makeAppForHealth(t)
	app.RestartPolicyJson = []byte(`{"mode":"none","max_per_window":5,"window_seconds":600}`)
	store := &fakeHealthStore{app: app}
	exec := &fakeExecutor{result: runtime.ExecResult{ExitCode: 1, Stdout: "fail"}}
	h := NewAppHealthCheckHandler(store, exec, &capturingNotifier{})
	job := sqlc.Job{Type: domain.JobTypeAppHealthCheck, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	if err := h.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(store.jobs) != 0 {
		t.Fatalf("mode=none 不应入队 restart; got %v", store.jobs)
	}
	if len(store.statusUpdates) != 0 {
		t.Fatalf("mode=none 不应推 error; got %v", store.statusUpdates)
	}
}
