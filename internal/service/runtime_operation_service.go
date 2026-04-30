package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// 与运行操作相关的错误。
var (
	ErrRuntimeOperationDenied = errors.New("无权执行运行操作")
)

// RuntimeOperationStore 抽象 service 需要的查询能力。
type RuntimeOperationStore interface {
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error)
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error)
}

// RuntimeOperationService 把启动/停止/重启/删除应用容器等高风险操作转化为 worker 任务。
//
// 高风险操作的含义：
//   - 这些操作真正修改 runtime node 上的容器状态，失败可能导致服务中断；
//   - 因此每次调用都会写一条审计日志，便于追溯触发人；
//   - 调度策略：worker 处理时按 app 状态机推进，不在 service 层直接修改 app.status。
type RuntimeOperationService struct {
	store RuntimeOperationStore
}

// NewRuntimeOperationService 创建运行操作服务。
func NewRuntimeOperationService(store RuntimeOperationStore) *RuntimeOperationService {
	return &RuntimeOperationService{store: store}
}

// RuntimeOperation 定义本服务支持的操作枚举。
type RuntimeOperation string

const (
	RuntimeOperationStart   RuntimeOperation = "start"
	RuntimeOperationStop    RuntimeOperation = "stop"
	RuntimeOperationRestart RuntimeOperation = "restart"
	RuntimeOperationDelete  RuntimeOperation = "delete"
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
	if !canTriggerRuntimeOperation(principal, app) {
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
		RunAfter:    pgtype.Timestamptz{Valid: false},
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
		Result:     "submitted",
	}); err != nil {
		return RuntimeOperationResult{}, fmt.Errorf("写入审计日志失败: %w", err)
	}
	return RuntimeOperationResult{JobID: uuidToString(job.ID), Operation: op}, nil
}

func isSupportedOperation(op RuntimeOperation) bool {
	switch op {
	case RuntimeOperationStart, RuntimeOperationStop, RuntimeOperationRestart, RuntimeOperationDelete:
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
	default:
		return ""
	}
}

func canTriggerRuntimeOperation(principal auth.Principal, app sqlc.App) bool {
	switch principal.Role {
	case domain.UserRolePlatformAdmin:
		return true
	case domain.UserRoleOrgAdmin:
		return principal.OrgID == uuidToString(app.OrgID)
	case domain.UserRoleOrgMember:
		if principal.UserID != uuidToString(app.OwnerUserID) {
			return false
		}
		// 普通成员被禁用账号也无权触发
		return true
	default:
		return false
	}
}
