package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/runtime"
	"oc-manager/internal/store/sqlc"
)

// AppHealthCheckStore 是 AppHealthCheckHandler 需要的 sqlc 子集。
type AppHealthCheckStore interface {
	AppRuntimeStore
	SetAppHealthState(ctx context.Context, arg sqlc.SetAppHealthStateParams) (sqlc.App, error)
}

// HealthCheckExecutor 抽象 worker 在容器内跑 healthz 探针的能力。
// 默认实现是 RuntimeAdapter.ContainerExec；测试中替换为内存桩。
type HealthCheckExecutor interface {
	ContainerExec(ctx context.Context, nodeID, containerID string, cmd []string) (runtime.ExecResult, error)
}

// healthCheckCmd 在 OpenClaw 容器内调本地 18789 /healthz。
// 跟 runtime/openclaw/healthcheck.sh 保持一致。
var healthCheckCmd = []string{"sh", "-c", "curl -fsS --max-time 5 http://127.0.0.1:18789/healthz"}

// restartPolicy 与 migration 0006 默认值一一对应；从 apps.restart_policy_json 解析。
type restartPolicy struct {
	Mode          string `json:"mode"`
	MaxPerWindow  int    `json:"max_per_window"`
	WindowSeconds int    `json:"window_seconds"`
}

// healthState 写入 apps.health_state_json：保留最近 N 次失败时间戳（不超过 max_per_window+1），
// 让窗口判断完全基于库内数据，避免 manager 重启后丢计数。
type healthState struct {
	LastSuccessAt time.Time   `json:"last_success_at,omitempty"`
	LastFailureAt time.Time   `json:"last_failure_at,omitempty"`
	LastError     string      `json:"last_error,omitempty"`
	Failures      []time.Time `json:"failures,omitempty"`
	RestartedAt   []time.Time `json:"restarted_at,omitempty"`
}

// AppHealthCheckHandler 周期跑容器内 /healthz 探针，记录健康状态并按 restart_policy 控制错误熔断。
//
// 处理流程：
//  1. load app（已删除/无容器/non-running 直接成功跳过）；
//  2. 解析 restart_policy + 现有 health_state；
//  3. ContainerExec /healthz：成功 → 写 last_success_at；失败 → append failures；
//  4. 失败次数 > max → 把 apps.status 推到 error，停止把短时卡顿误判成可自动重启故障。
//
// 任意环节冒泡的错误只标记为 job 失败由 worker 重试；handler 自身保持幂等。
type AppHealthCheckHandler struct {
	store    AppHealthCheckStore
	executor HealthCheckExecutor
	now      func() time.Time
}

// NewAppHealthCheckHandler 创建 handler。
func NewAppHealthCheckHandler(store AppHealthCheckStore, executor HealthCheckExecutor) *AppHealthCheckHandler {
	return &AppHealthCheckHandler{store: store, executor: executor, now: time.Now}
}

// Handle 执行 app_health_check job。
func (h *AppHealthCheckHandler) Handle(ctx context.Context, job sqlc.Job) error {
	if job.Type != domain.JobTypeAppHealthCheck {
		return fmt.Errorf("非 app_health_check 任务: %s", job.Type)
	}
	payload, err := decodeAppOpPayload(job.PayloadJson)
	if err != nil {
		return err
	}
	app, _, err := loadApp(ctx, h.store, payload)
	if err != nil {
		return err
	}
	if app.Status != domain.AppStatusRunning {
		// 仅对 running 状态做健康检查：binding_waiting 时容器虽起但 OpenClaw 还没就绪，会假阳性。
		return nil
	}
	if app.ContainerID.String == "" || !app.RuntimeNodeID.Valid {
		return nil
	}
	policy := decodeRestartPolicy(app.RestartPolicyJson)
	state := decodeHealthState(app.HealthStateJson)
	now := h.now()

	nodeID := uuidToString(app.RuntimeNodeID)
	exec, execErr := h.executor.ContainerExec(ctx, nodeID, app.ContainerID.String, healthCheckCmd)
	if execErr != nil || exec.ExitCode != 0 {
		state.LastFailureAt = now
		if execErr != nil {
			state.LastError = sanitizeHealthStateText(execErr.Error())
		} else {
			state.LastError = sanitizeHealthStateText(fmt.Sprintf("exit=%d %s", exec.ExitCode, truncate(exec.Stdout, 200)))
		}
		state.Failures = appendWithinWindow(state.Failures, now, policy)
		if exhaustedRestartBudget(policy, len(state.Failures)) {
			if _, err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusError}); err != nil {
				return fmt.Errorf("更新应用状态失败: %w", err)
			}
		}
	} else {
		state.LastSuccessAt = now
		state.LastError = ""
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("序列化 health state 失败: %w", err)
	}
	if _, err := h.store.SetAppHealthState(ctx, sqlc.SetAppHealthStateParams{
		ID:              pgtype.UUID{Bytes: app.ID.Bytes, Valid: true},
		HealthStateJson: encoded,
	}); err != nil {
		return fmt.Errorf("写入 health state 失败: %w", err)
	}
	return nil
}

func decodeRestartPolicy(raw []byte) restartPolicy {
	out := restartPolicy{Mode: "on_failure", MaxPerWindow: 5, WindowSeconds: 600}
	if len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	if out.MaxPerWindow <= 0 {
		out.MaxPerWindow = 5
	}
	if out.WindowSeconds <= 0 {
		out.WindowSeconds = 600
	}
	if out.Mode == "" {
		out.Mode = "on_failure"
	}
	return out
}

func decodeHealthState(raw []byte) healthState {
	var out healthState
	if len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

// appendWithinWindow 把 ts 追加到列表，并按 windowSeconds 截断老条目。
// 列表本身不超过 max_per_window+1，避免 jsonb 无限膨胀。
func appendWithinWindow(list []time.Time, ts time.Time, policy restartPolicy) []time.Time {
	cutoff := ts.Add(-time.Duration(policy.WindowSeconds) * time.Second)
	out := make([]time.Time, 0, len(list)+1)
	for _, t := range list {
		if t.After(cutoff) {
			out = append(out, t)
		}
	}
	out = append(out, ts)
	if maxKeep := policy.MaxPerWindow + 1; len(out) > maxKeep {
		out = out[len(out)-maxKeep:]
	}
	return out
}

// exhaustedRestartBudget 判断是否需要把 apps.status 推到 error 锁死。
func exhaustedRestartBudget(policy restartPolicy, failures int) bool {
	if policy.Mode == "none" {
		return false
	}
	return failures > policy.MaxPerWindow
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(s[:n]) + "…"
}

func sanitizeHealthStateText(s string) string {
	// PostgreSQL jsonb 不接受 \u0000；健康检查失败文本来自容器 stdout / error，
	// 写库前需要清洗，避免“记录失败状态”本身把 job 打失败。
	return strings.ReplaceAll(s, "\x00", "�")
}
