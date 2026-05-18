// Package audit 提供跨 service 的审计辅助能力。
// 当前只封装 new-api 调用失败记录，确保 API handler 与后台 worker 走同一套失败落库语义。
package audit

import (
	"context"
	"fmt"
	"log/slog"

	"oc-manager/internal/service"
)

// AuditRecorder 抽象审计写入能力。仅依赖 service.AuditEvent / AuditResult，
// 这样测试可以注入 fake 而不必引入完整 service.AuditService。
type AuditRecorder interface {
	Record(ctx context.Context, event service.AuditEvent) (service.AuditResult, error)
}

// NewAPIFailureContext 描述一次 new-api 调用失败的上下文。
//
// ActorID / ActorRole / OrgID 在 API 请求路径有 user context 时填，
// worker 后台路径不填，由 helper 自动 fallback 到 ActorRole=system。
type NewAPIFailureContext struct {
	// ActorID 是触发调用的 manager 用户；后台 worker 为空。
	ActorID string
	// ActorRole 是触发者角色；为空时 RecordFailure 回退为 system。
	ActorRole string
	// OrgID 是失败调用所属组织；平台级操作可为空。
	OrgID string
	// Endpoint 是上游接口标识，如 "POST /api/user/"。
	Endpoint string
	// Status 是上游 HTTP 状态码；未知或未发出请求时保持 0。
	Status int
	// Err 是失败原因，会写入 ErrorMessage；nil 时只记录端点和状态。
	Err error
}

// NewAPIAuditHelper 把 new-api 失败统一落到 audit_logs.target_type=newapi_call。
//
// 设计要点：
//   - 失败本身不阻塞业务；helper 内部吞掉 audit Record 的错误，仅记 Stderr 日志。
//   - target_type 用 "newapi_call"（audit_logs.target_type 是无 CHECK 的 text 列）。
//   - actor 字段：API 路径有 user context 时直传；worker 路径不传 → 默认 actor_role=system。
type NewAPIAuditHelper struct {
	recorder AuditRecorder
}

// NewNewAPIAuditHelper 构造 helper；recorder 通常是 *service.AuditService。
func NewNewAPIAuditHelper(recorder AuditRecorder) *NewAPIAuditHelper {
	return &NewAPIAuditHelper{recorder: recorder}
}

// RecordNewAPIFailure 实现 service.NewAPIFailureAuditor 接口，
// 将 service.NewAPIFailureContext 转换为 audit.NewAPIFailureContext 后调 RecordFailure。
// 通过此方法，*NewAPIAuditHelper 可直接注入 OrganizationService，无需 service 包反向依赖 audit 包。
func (h *NewAPIAuditHelper) RecordNewAPIFailure(ctx context.Context, fc service.NewAPIFailureContext) {
	h.RecordFailure(ctx, NewAPIFailureContext{
		ActorID:   fc.ActorID,
		ActorRole: fc.ActorRole,
		OrgID:     fc.OrgID,
		Endpoint:  fc.Endpoint,
		Err:       fc.Err,
	})
}

// RecordFailure 写一条 newapi_call 失败审计。
//
// 不返回 error：本 helper 是"附属操作"，主流程不应因审计失败而失败。
// 底层 Record 报错时仅打日志并继续，不阻断主流程。
func (h *NewAPIAuditHelper) RecordFailure(ctx context.Context, fc NewAPIFailureContext) {
	if h == nil || h.recorder == nil {
		return
	}
	actorRole := fc.ActorRole
	if actorRole == "" {
		// 后台 worker 没有用户上下文，统一归为 system，避免 audit_logs.actor_role 为空导致排障困难。
		actorRole = "system"
	}
	msg := ""
	if fc.Err != nil {
		msg = fc.Err.Error()
	}
	metadata := map[string]any{
		"endpoint":    fc.Endpoint,
		"status_code": fc.Status,
	}
	// 详情字段附带 HTTP 状态码，便于审计列表识别失败原因；status=0 表示未发出请求 / 未知状态，留空。
	detail := ""
	if fc.Status > 0 {
		detail = fmt.Sprintf("HTTP %d", fc.Status)
	}
	event := service.AuditEvent{
		ActorID:       fc.ActorID,
		ActorRole:     actorRole,
		OrgID:         fc.OrgID,
		TargetType:    "newapi_call",
		TargetID:      fc.Endpoint,
		Action:        fc.Endpoint,
		Result:        "failed",
		ErrorMessage:  msg,
		Metadata:      metadata,
		DetailMessage: detail,
	}
	if _, err := h.recorder.Record(ctx, event); err != nil {
		slog.ErrorContext(ctx, "写 audit_logs 失败", "error", err)
	}
}
