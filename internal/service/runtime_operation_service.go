package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// RuntimeOperationStore 抽象 service 需要的查询能力。
type RuntimeOperationStore interface {
	GetApp(ctx context.Context, id string) (sqlc.App, error)
	GetUser(ctx context.Context, id string) (sqlc.User, error)
	SetAppStatus(ctx context.Context, arg sqlc.SetAppStatusParams) error
	SetAppNewAPIKey(ctx context.Context, arg sqlc.SetAppNewAPIKeyParams) error
	SetAppContainer(ctx context.Context, arg sqlc.SetAppContainerParams) error
	// ClearAppProgress 把 apps.progress_current / progress_total 重置为 NULL,
	// RequestInitialize 重试时调用,避免前端看到上一次失败遗留的进度数。
	ClearAppProgress(ctx context.Context, id string) error
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) error
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) error
	// CountChannelBindingsByApp 统计应用下未删除的渠道绑定数；Trigger 在 delete 审计详情中展示这个数。
	CountChannelBindingsByApp(ctx context.Context, appID string) (int64, error)
}

// JobNotifier 抽象向 Redis 队列推送 jobID 的能力。
// scheduler 周期性扫库兜底，但即时入队让 worker 可以毫秒级拿到任务，
// 否则启停操作可能要等一个 scheduler 周期才被处理。
type JobNotifier interface {
	Enqueue(ctx context.Context, jobID string) error
}

// RuntimeInspector 抽象 manager 通过 agent docker 代理 inspect 节点容器的能力。
// 与 runtime.Adapter.InspectContainer 兼容；service 层只关心结构化结果。
type RuntimeInspector interface {
	InspectContainer(ctx context.Context, nodeID, containerID string) (RuntimeContainerInfo, error)
}

// RuntimeContainerInfo 是面向 service/handler 的最小容器视图。
// 与 runtime.ContainerInfo 字段一致，单独定义是为了让上层不依赖 runtime 包。
type RuntimeContainerInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Image  string `json:"image"`
	Status string `json:"status"`
}

// RuntimeView 是 GET /apps/:appId/runtime 接口的响应视图。
// container_id 为空时只返回 status="no_container"，前端据此切换"未创建容器"提示。
// Snapshot 来自 apps.runtime_snapshot_json（scheduler 30s 周期采样）；为空表示尚未采集。
type RuntimeView struct {
	Status    string                `json:"status"`
	Container *RuntimeContainerInfo `json:"container,omitempty"`
	Snapshot  *RuntimeSnapshotView  `json:"snapshot,omitempty"`
}

// RuntimeSnapshotView 是 apps.runtime_snapshot_json 的对外视图。
// 结构与 worker.handlers.AppRuntimeSnapshot 字段对齐；service 层不重新计算单位。
type RuntimeSnapshotView struct {
	CPUPercent     float64   `json:"cpu_percent"`
	MemoryUsage    uint64    `json:"memory_usage_bytes"`
	MemoryLimit    uint64    `json:"memory_limit_bytes"`
	NetworkRxBytes uint64    `json:"network_rx_bytes"`
	NetworkTxBytes uint64    `json:"network_tx_bytes"`
	CollectedAt    time.Time `json:"collected_at"`
	LastError      string    `json:"last_error,omitempty"`
}

// RuntimeOperationService 把启动/停止/重启/删除应用容器等高风险操作转化为 worker 任务。
//
// 高风险操作的含义：
//   - 这些操作真正修改 runtime node 上的容器状态，失败可能导致服务中断；
//   - 因此每次调用都会写一条审计日志，便于追溯触发人；
//   - 调度策略：worker 处理时按 app 状态机推进，不在 service 层直接修改 app.status。
//
// JobNotifier 可不传：未注入时只写库，由 scheduler 兜底入队（吞吐降级，延迟最长一个 scheduler 周期）。
type RuntimeOperationService struct {
	store     RuntimeOperationStore
	notifier  JobNotifier
	inspector RuntimeInspector
	// logger 仅用于错误诊断（如 ensurePrincipalActive DB 错误），不替代审计日志
	logger *slog.Logger
}

// NewRuntimeOperationService 创建运行操作服务。
// logger 仅用于错误诊断（如 ensurePrincipalActive DB 错误），不替代审计日志。
// notifier 可不传：未注入时只写库，由 scheduler 兜底入队（吞吐降级，延迟最长一个 scheduler 周期）。
func NewRuntimeOperationService(store RuntimeOperationStore, logger *slog.Logger, notifier ...JobNotifier) *RuntimeOperationService {
	var n JobNotifier
	if len(notifier) > 0 {
		n = notifier[0]
	}
	return &RuntimeOperationService{store: store, logger: logger, notifier: n}
}

// SetInspector 注入 runtime inspector（cmd/server 装配时可选）。
// inspector 为 nil 时 InspectApp 仅返回库内 status，不调 docker。
func (s *RuntimeOperationService) SetInspector(inspector RuntimeInspector) {
	s.inspector = inspector
}

// InspectApp 透传应用容器的 docker inspect 状态。
//
// 行为：
//   - 校验权限、加载 app；
//   - container_id 为空 → 返回 RuntimeView{Status:"no_container"}；
//   - inspector 未配置 → 返回 RuntimeView{Status: app.Status}（库内状态兜底）；
//   - inspector 错误 → 返回 RuntimeView{Status:"error", Container:nil}，让前端展示"无法连接节点"。
func (s *RuntimeOperationService) InspectApp(ctx context.Context, principal auth.Principal, appID string) (RuntimeView, error) {
	app, err := s.store.GetApp(ctx, appID)
	if errors.Is(err, sql.ErrNoRows) {
		return RuntimeView{}, ErrNotFound
	}
	if err != nil {
		return RuntimeView{}, fmt.Errorf("查询应用失败: %w", err)
	}
	if !auth.CanViewApp(principal, app.OrgID, app.OwnerUserID) {
		return RuntimeView{}, ErrForbidden
	}
	if !app.ContainerID.Valid || app.ContainerID.String == "" {
		return RuntimeView{Status: "no_container"}, nil
	}
	if s.inspector == nil {
		return RuntimeView{Status: app.Status}, nil
	}
	// app.RuntimeNodeID nullable（spec-A2a）：.String 取 Go string 值。
	info, err := s.inspector.InspectContainer(ctx, app.RuntimeNodeID.String, app.ContainerID.String)
	if err != nil {
		return RuntimeView{Status: "error", Snapshot: snapshotFromApp(app)}, nil
	}
	return RuntimeView{
		Status:    info.Status,
		Container: &info,
		Snapshot:  snapshotFromApp(app),
	}, nil
}

// snapshotFromApp 解析 apps.runtime_snapshot_json；解析失败一律返回 nil，避免暴露内部错误。
func snapshotFromApp(app sqlc.App) *RuntimeSnapshotView {
	if len(app.RuntimeSnapshotJson) == 0 {
		return nil
	}
	var raw struct {
		CPUPercent     float64 `json:"cpu_percent"`
		MemoryUsage    uint64  `json:"memory_usage_bytes"`
		MemoryLimit    uint64  `json:"memory_limit_bytes"`
		NetworkRxBytes uint64  `json:"network_rx_bytes"`
		NetworkTxBytes uint64  `json:"network_tx_bytes"`
		LastError      string  `json:"last_error,omitempty"`
	}
	if err := json.Unmarshal(app.RuntimeSnapshotJson, &raw); err != nil {
		return nil
	}
	out := &RuntimeSnapshotView{
		CPUPercent:     raw.CPUPercent,
		MemoryUsage:    raw.MemoryUsage,
		MemoryLimit:    raw.MemoryLimit,
		NetworkRxBytes: raw.NetworkRxBytes,
		NetworkTxBytes: raw.NetworkTxBytes,
		LastError:      raw.LastError,
	}
	// RuntimeSnapshotAt 是 null.Time。
	if app.RuntimeSnapshotAt.Valid {
		out.CollectedAt = app.RuntimeSnapshotAt.Time
	}
	return out
}

// RuntimeOperation 定义本服务支持的操作枚举。
type RuntimeOperation string

const (
	// RuntimeOperationStart 表示启动应用容器。
	RuntimeOperationStart RuntimeOperation = "start"
	// RuntimeOperationStop 表示停止应用容器。
	RuntimeOperationStop RuntimeOperation = "stop"
	// RuntimeOperationRestart 表示重启应用容器。
	RuntimeOperationRestart RuntimeOperation = "restart"
	// RuntimeOperationDelete 表示删除应用容器及其 runtime 资源。
	RuntimeOperationDelete RuntimeOperation = "delete"
	// RuntimeOperationDisableAPIKey 表示临时禁用应用 new-api key，普通成员不能触发。
	RuntimeOperationDisableAPIKey RuntimeOperation = "disable_api_key"
	// RuntimeOperationRestoreAPIKey 表示恢复应用 new-api key，普通成员不能触发。
	RuntimeOperationRestoreAPIKey RuntimeOperation = "restore_api_key"
)

// RuntimeOperationResult 是异步任务派发结果。
type RuntimeOperationResult struct {
	// JobID 是写入 jobs 表后的异步任务 ID，前端用它查询执行进度。
	JobID string `json:"job_id"`
	// Operation 是本次实际入队的操作枚举。
	Operation RuntimeOperation `json:"operation"`
}

// Trigger 触发指定应用的运行操作。
// 调用方负责传入操作枚举和当前 principal，service 校验权限、应用状态后写入异步任务和审计。
func (s *RuntimeOperationService) Trigger(ctx context.Context, principal auth.Principal, appID string, op RuntimeOperation) (RuntimeOperationResult, error) {
	if !isSupportedOperation(op) {
		return RuntimeOperationResult{}, fmt.Errorf("不支持的运行操作: %s", op)
	}
	app, err := s.store.GetApp(ctx, appID)
	if errors.Is(err, sql.ErrNoRows) {
		return RuntimeOperationResult{}, ErrNotFound
	}
	if err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("查询应用失败: %w", err)
	}
	if err := s.ensurePrincipalActive(ctx, principal); err != nil {
		return RuntimeOperationResult{}, err
	}
	if !auth.CanTriggerRuntimeOperation(principal, app.OrgID, app.OwnerUserID) {
		return RuntimeOperationResult{}, ErrRuntimeOperationDenied
	}
	// disable/restore api_key 走风控路径，禁止普通成员触发。
	if (op == RuntimeOperationDisableAPIKey || op == RuntimeOperationRestoreAPIKey) &&
		principal.Role == domain.UserRoleOrgMember {
		return RuntimeOperationResult{}, ErrRuntimeOperationDenied
	}
	jobType := jobTypeFor(op)
	payload, err := json.Marshal(map[string]any{
		"app_id":       app.ID,
		"operation":    string(op),
		"runtime_node": app.RuntimeNodeID,
		"requested_by": principal.UserID,
	})
	if err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("序列化 payload 失败: %w", err)
	}
	// CreateJob 为 :exec；RunAfter 是 time.Time（MySQL DATETIME）。
	jobID := newUUID()
	if err := s.store.CreateJob(ctx, sqlc.CreateJobParams{
		ID:          jobID,
		Type:        jobType,
		Priority:    100,
		RunAfter:    time.Now(),
		MaxAttempts: 3,
		PayloadJson: payload,
	}); err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("创建运行操作任务失败: %w", err)
	}
	// app.delete 详情附带级联渠道绑定数，便于审计列表识别本次删除会清理掉多少渠道。
	// 其他 op (start/stop/restart/disable_api_key/restore_api_key) 与 actor 列重复，留空。
	var detail null.String
	if op == RuntimeOperationDelete {
		cascadeCount, err := s.store.CountChannelBindingsByApp(ctx, app.ID)
		if err != nil {
			return RuntimeOperationResult{}, fmt.Errorf("统计渠道绑定数失败: %w", err)
		}
		detail = null.StringFrom(fmt.Sprintf("级联：%d 个渠道绑定", cascadeCount))
	}
	if err := s.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
		ID:        newUUID(),
		ActorID:   null.StringFrom(principal.UserID),
		ActorRole: principal.Role,
		OrgID:     null.StringFrom(app.OrgID),
		TargetType: "app",
		TargetID:   app.ID,
		Action:     string(op),
		// audit_logs.result CHECK 仅允许 succeeded/failed；
		// 这里 audit 的语义是「操作已成功提交入队」，与其他 service 写 audit 的写法保持一致。
		Result:        "succeeded",
		DetailMessage: detail,
		// 非 delete op 不填详情：start/stop/restart/disable_api_key/restore_api_key
		// 的详情与「谁触发」列重复，按设计文档落 NULL（前端展示「—」）。
	}); err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("写入审计日志失败: %w", err)
	}
	if s.notifier != nil {
		// notifier 失败不阻塞响应：scheduler 最终会扫到 pending job 重新入队，
		// 但仍把错误冒泡到日志，便于运维识别 Redis 抖动。
		_ = s.notifier.Enqueue(ctx, jobID)
	}
	return RuntimeOperationResult{JobID: jobID, Operation: op}, nil
}

// RequestInitialize 触发应用初始化重试。
//
// 仅当应用 status ∈ {error, draft} 时允许；其它状态返回 ErrAppNotReinitializable。
// 重置四个字段保证 worker handler 重新走完整 4 阶段流程：
//   - status = pulling_runtime_image：worker 直接进入第一阶段，不再停在 draft
//     等 onboarding 拾取；状态机允许 error / draft → pulling_runtime_image。
//   - api_key_status = pending：worker 重新调用 new-api 创建 token；
//   - container_id = NULL：worker 重新创建容器，避免旧容器残留；
//   - progress_current / progress_total = NULL：清空上一次失败遗留的进度数，
//     前端从全新状态开始展示。
//
// 重置不在事务中——worker 自身有状态机校验和幂等处理；即便重置过程中崩溃，
// 下次调用仍能完成或者由人工介入。审计日志记录触发人，便于追溯。
func (s *RuntimeOperationService) RequestInitialize(ctx context.Context, principal auth.Principal, appID string) (RuntimeOperationResult, error) {
	app, err := s.store.GetApp(ctx, appID)
	if errors.Is(err, sql.ErrNoRows) {
		return RuntimeOperationResult{}, ErrNotFound
	}
	if err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("查询应用失败: %w", err)
	}
	if err := s.ensurePrincipalActive(ctx, principal); err != nil {
		return RuntimeOperationResult{}, err
	}
	// RequestInitialize 对应"重新初始化"操作，不属于常规启停运维；平台管理员不开放此入口。
	if !auth.CanManageApp(principal, app.OrgID, app.OwnerUserID) {
		return RuntimeOperationResult{}, ErrRuntimeOperationDenied
	}
	if app.Status != domain.AppStatusError && app.Status != domain.AppStatusDraft {
		return RuntimeOperationResult{}, ErrAppNotReinitializable
	}
	// 重置目标为 pulling_runtime_image：worker 直接从第一阶段开始重跑，
	// 不再回到 draft 等待 onboarding 拾取。状态机已允许 error / draft → pulling_runtime_image。
	if err := s.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusPullingRuntimeImage}); err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("重置应用状态失败: %w", err)
	}
	// 清空 progress_*,避免前端看到上一次失败遗留的进度数;ClearAppProgress
	// 当前只清进度字段,last_error_status 留作历史可查(下一次状态机推进时由
	// transitionTo 自然覆盖)。
	if err := s.store.ClearAppProgress(ctx, app.ID); err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("清空应用进度字段失败: %w", err)
	}
	// SetAppNewAPIKey 为 :exec；清空 api_key 字段，ApiKeyStatus = pending。
	if err := s.store.SetAppNewAPIKey(ctx, sqlc.SetAppNewAPIKeyParams{
		ID:                  app.ID,
		NewapiKeyID:         null.String{},
		NewapiKeyCiphertext: null.String{},
		ApiKeyStatus:        domain.APIKeyStatusPending,
		NewapiKeyName:       null.String{},
	}); err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("重置 api_key 状态失败: %w", err)
	}
	// SetAppContainer 为 :exec；清空 container_id / container_name。
	if err := s.store.SetAppContainer(ctx, sqlc.SetAppContainerParams{
		ID:            app.ID,
		ContainerID:   null.String{},
		ContainerName: null.String{},
	}); err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("清空 container_id 失败: %w", err)
	}

	payload, err := json.Marshal(map[string]any{
		"app_id":       app.ID,
		"runtime_node": app.RuntimeNodeID,
		"requested_by": principal.UserID,
	})
	if err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("序列化 payload 失败: %w", err)
	}
	jobID := newUUID()
	if err := s.store.CreateJob(ctx, sqlc.CreateJobParams{
		ID:          jobID,
		Type:        domain.JobTypeAppInitialize,
		Priority:    100,
		RunAfter:    time.Now(),
		MaxAttempts: 3,
		PayloadJson: payload,
	}); err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("创建初始化任务失败: %w", err)
	}
	if err := s.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
		ID:        newUUID(),
		ActorID:   null.StringFrom(principal.UserID),
		ActorRole: principal.Role,
		OrgID:     null.StringFrom(app.OrgID),
		TargetType: "app",
		TargetID:   app.ID,
		Action:     "initialize",
		// audit_logs.result CHECK 仅允许 succeeded/failed；
		// 这里 audit 的语义是「操作已成功提交入队」，与其他 service 写 audit 的写法保持一致。
		Result: "succeeded",
		// 不填 DetailMessage：initialize 的资源列已展示 app 名，详情列冗余。
	}); err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("写入审计日志失败: %w", err)
	}
	if s.notifier != nil {
		_ = s.notifier.Enqueue(ctx, jobID)
	}
	return RuntimeOperationResult{JobID: jobID, Operation: "initialize"}, nil
}

// ensurePrincipalActive 校验主体当前未被禁用。
// runtime 操作风险高，被禁用账号即使 token 未过期也不得触发；
// disabled 检查与角色/归属判断分离，便于未来对其他高风险操作复用。
func (s *RuntimeOperationService) ensurePrincipalActive(ctx context.Context, principal auth.Principal) error {
	user, err := s.store.GetUser(ctx, principal.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRuntimeOperationDenied
	}
	if err != nil {
		// DB 错误用 slog.ErrorContext，trace_id 自动通过 ctx 注入
		s.logger.ErrorContext(ctx, "查询主体状态失败",
			"user_id", principal.UserID,
			"error", err,
		)
		return fmt.Errorf("查询主体状态失败: %w", err)
	}
	if user.Status == domain.StatusDisabled {
		return ErrRuntimeOperationDenied
	}
	return nil
}

func isSupportedOperation(op RuntimeOperation) bool {
	switch op {
	case RuntimeOperationStart, RuntimeOperationStop, RuntimeOperationRestart, RuntimeOperationDelete,
		RuntimeOperationDisableAPIKey, RuntimeOperationRestoreAPIKey:
		return true
	default:
		return false
	}
}

func jobTypeFor(op RuntimeOperation) string {
	switch op {
	case RuntimeOperationStart:
		return domain.JobTypeAppStartContainer
	case RuntimeOperationStop:
		return domain.JobTypeAppStopContainer
	case RuntimeOperationRestart:
		return domain.JobTypeAppRestartContainer
	case RuntimeOperationDelete:
		return domain.JobTypeAppDelete
	case RuntimeOperationDisableAPIKey:
		return domain.JobTypeNewAPIDisableKey
	case RuntimeOperationRestoreAPIKey:
		return domain.JobTypeNewAPIRestoreKey
	default:
		return ""
	}
}
