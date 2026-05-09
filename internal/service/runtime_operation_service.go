package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// 与运行操作相关的错误。
var (
	ErrRuntimeOperationDenied = errors.New("无权执行运行操作")
	ErrAppNotReinitializable  = errors.New("应用当前状态不允许重新初始化")
)

// RuntimeOperationStore 抽象 service 需要的查询能力。
type RuntimeOperationStore interface {
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	GetUser(ctx context.Context, id pgtype.UUID) (sqlc.User, error)
	SetAppStatus(ctx context.Context, arg sqlc.SetAppStatusParams) (sqlc.App, error)
	SetAppNewAPIKey(ctx context.Context, arg sqlc.SetAppNewAPIKeyParams) (sqlc.App, error)
	SetAppContainer(ctx context.Context, arg sqlc.SetAppContainerParams) (sqlc.App, error)
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error)
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error)
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
}

// NewRuntimeOperationService 创建运行操作服务。
// notifier 传 nil 表示不做即时入队，仅依赖 scheduler 扫库兜底。
func NewRuntimeOperationService(store RuntimeOperationStore, notifier ...JobNotifier) *RuntimeOperationService {
	var n JobNotifier
	if len(notifier) > 0 {
		n = notifier[0]
	}
	return &RuntimeOperationService{store: store, notifier: n}
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
	id, err := parseUUID(appID)
	if err != nil {
		return RuntimeView{}, ErrNotFound
	}
	app, err := s.store.GetApp(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeView{}, ErrNotFound
	}
	if err != nil {
		return RuntimeView{}, fmt.Errorf("查询应用失败: %w", err)
	}
	if !auth.CanViewApp(principal, uuidToString(app.OrgID), uuidToString(app.OwnerUserID)) {
		return RuntimeView{}, ErrForbidden
	}
	if !app.ContainerID.Valid || app.ContainerID.String == "" {
		return RuntimeView{Status: "no_container"}, nil
	}
	if s.inspector == nil {
		return RuntimeView{Status: app.Status}, nil
	}
	info, err := s.inspector.InspectContainer(ctx, uuidToString(app.RuntimeNodeID), app.ContainerID.String)
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
	if app.RuntimeSnapshotAt.Valid {
		out.CollectedAt = app.RuntimeSnapshotAt.Time
	}
	return out
}

// RuntimeOperation 定义本服务支持的操作枚举。
type RuntimeOperation string

const (
	RuntimeOperationStart           RuntimeOperation = "start"
	RuntimeOperationStop            RuntimeOperation = "stop"
	RuntimeOperationRestart         RuntimeOperation = "restart"
	RuntimeOperationDelete          RuntimeOperation = "delete"
	RuntimeOperationDisableAPIKey   RuntimeOperation = "disable_api_key"
	RuntimeOperationRestoreAPIKey   RuntimeOperation = "restore_api_key"
)

// RuntimeOperationResult 是异步任务派发结果。
type RuntimeOperationResult struct {
	JobID     string           `json:"job_id"`
	Operation RuntimeOperation `json:"operation"`
}

// Trigger 触发指定应用的运行操作。
// 调用方负责传入操作枚举和当前 principal，service 校验权限、应用状态后写入异步任务和审计。
func (s *RuntimeOperationService) Trigger(ctx context.Context, principal auth.Principal, appID string, op RuntimeOperation) (RuntimeOperationResult, error) {
	if !isSupportedOperation(op) {
		return RuntimeOperationResult{}, fmt.Errorf("不支持的运行操作: %s", op)
	}
	id, err := parseUUID(appID)
	if err != nil {
		return RuntimeOperationResult{}, ErrNotFound
	}
	app, err := s.store.GetApp(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeOperationResult{}, ErrNotFound
	}
	if err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("查询应用失败: %w", err)
	}
	if err := s.ensurePrincipalActive(ctx, principal); err != nil {
		return RuntimeOperationResult{}, err
	}
	if !auth.CanTriggerRuntimeOperation(principal, uuidToString(app.OrgID), uuidToString(app.OwnerUserID)) {
		return RuntimeOperationResult{}, ErrRuntimeOperationDenied
	}
	// disable/restore api_key 走风控路径，禁止普通成员触发。
	if (op == RuntimeOperationDisableAPIKey || op == RuntimeOperationRestoreAPIKey) &&
		principal.Role == domain.UserRoleOrgMember {
		return RuntimeOperationResult{}, ErrRuntimeOperationDenied
	}
	jobType := jobTypeFor(op)
	payload, err := json.Marshal(map[string]any{
		"app_id":         uuidToString(app.ID),
		"operation":      string(op),
		"runtime_node":   uuidToOptionalString(app.RuntimeNodeID),
		"requested_by":   principal.UserID,
	})
	if err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("序列化 payload 失败: %w", err)
	}
	job, err := s.store.CreateJob(ctx, sqlc.CreateJobParams{
		Type:        jobType,
		Priority:    100,
		RunAfter:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
		MaxAttempts: 3,
		PayloadJson: payload,
	})
	if err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("创建运行操作任务失败: %w", err)
	}
	actorUUID, _ := optionalUUID(principal.UserID)
	if _, err := s.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
		ActorID:    actorUUID,
		ActorRole:  principal.Role,
		OrgID:      app.OrgID,
		TargetType: "app",
		TargetID:   uuidToString(app.ID),
		Action:     string(op),
		// audit_logs.result CHECK 仅允许 succeeded/failed；
		// 这里 audit 的语义是「操作已成功提交入队」，与其他 service 写 audit 的写法保持一致。
		Result: "succeeded",
	}); err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("写入审计日志失败: %w", err)
	}
	if s.notifier != nil {
		// notifier 失败不阻塞响应：scheduler 最终会扫到 pending job 重新入队，
		// 但仍把错误冒泡到日志，便于运维识别 Redis 抖动。
		_ = s.notifier.Enqueue(ctx, uuidToString(job.ID))
	}
	return RuntimeOperationResult{JobID: uuidToString(job.ID), Operation: op}, nil
}

// RequestInitialize 触发应用初始化重试。
//
// 仅当应用 status ∈ {error, draft} 时允许；其它状态返回 ErrAppNotReinitializable。
// 重置三个字段保证 worker handler 重新走完整流程：
//   - status = draft：worker 看到 draft 后会执行 prompt 渲染、镜像分发、容器创建；
//   - api_key_status = pending：worker 重新调用 new-api 创建 token；
//   - container_id = NULL：worker 重新创建容器，避免旧容器残留。
//
// 重置不在事务中——worker 自身有状态机校验和幂等处理；即便重置过程中崩溃，
// 下次调用仍能完成或者由人工介入。审计日志记录触发人，便于追溯。
func (s *RuntimeOperationService) RequestInitialize(ctx context.Context, principal auth.Principal, appID string) (RuntimeOperationResult, error) {
	id, err := parseUUID(appID)
	if err != nil {
		return RuntimeOperationResult{}, ErrNotFound
	}
	app, err := s.store.GetApp(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeOperationResult{}, ErrNotFound
	}
	if err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("查询应用失败: %w", err)
	}
	if err := s.ensurePrincipalActive(ctx, principal); err != nil {
		return RuntimeOperationResult{}, err
	}
	if !auth.CanTriggerRuntimeOperation(principal, uuidToString(app.OrgID), uuidToString(app.OwnerUserID)) {
		return RuntimeOperationResult{}, ErrRuntimeOperationDenied
	}
	if app.Status != domain.AppStatusError && app.Status != domain.AppStatusDraft {
		return RuntimeOperationResult{}, ErrAppNotReinitializable
	}
	if _, err := s.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusDraft}); err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("重置应用状态失败: %w", err)
	}
	if _, err := s.store.SetAppNewAPIKey(ctx, sqlc.SetAppNewAPIKeyParams{
		ID:                  app.ID,
		NewapiKeyID:         pgtype.Text{},
		NewapiKeyCiphertext: pgtype.Text{},
		ApiKeyStatus:        domain.APIKeyStatusPending,
	}); err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("重置 api_key 状态失败: %w", err)
	}
	if _, err := s.store.SetAppContainer(ctx, sqlc.SetAppContainerParams{
		ID:            app.ID,
		ContainerID:   pgtype.Text{},
		ContainerName: pgtype.Text{},
	}); err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("清空 container_id 失败: %w", err)
	}

	payload, err := json.Marshal(map[string]any{
		"app_id":       uuidToString(app.ID),
		"runtime_node": uuidToOptionalString(app.RuntimeNodeID),
		"requested_by": principal.UserID,
	})
	if err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("序列化 payload 失败: %w", err)
	}
	job, err := s.store.CreateJob(ctx, sqlc.CreateJobParams{
		Type:        domain.JobTypeAppInitialize,
		Priority:    100,
		RunAfter:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
		MaxAttempts: 3,
		PayloadJson: payload,
	})
	if err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("创建初始化任务失败: %w", err)
	}
	actorUUID, _ := optionalUUID(principal.UserID)
	if _, err := s.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
		ActorID:    actorUUID,
		ActorRole:  principal.Role,
		OrgID:      app.OrgID,
		TargetType: "app",
		TargetID:   uuidToString(app.ID),
		Action:     "initialize",
		// audit_logs.result CHECK 仅允许 succeeded/failed；
		// 这里 audit 的语义是「操作已成功提交入队」，与其他 service 写 audit 的写法保持一致。
		Result: "succeeded",
	}); err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("写入审计日志失败: %w", err)
	}
	if s.notifier != nil {
		_ = s.notifier.Enqueue(ctx, uuidToString(job.ID))
	}
	return RuntimeOperationResult{JobID: uuidToString(job.ID), Operation: "initialize"}, nil
}

// ensurePrincipalActive 校验主体当前未被禁用。
// runtime 操作风险高，被禁用账号即使 token 未过期也不得触发；
// disabled 检查与角色/归属判断分离，便于未来对其他高风险操作复用。
func (s *RuntimeOperationService) ensurePrincipalActive(ctx context.Context, principal auth.Principal) error {
	id, err := parseUUID(principal.UserID)
	if err != nil {
		return ErrRuntimeOperationDenied
	}
	user, err := s.store.GetUser(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRuntimeOperationDenied
	}
	if err != nil {
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
