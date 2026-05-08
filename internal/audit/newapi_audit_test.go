package audit_test

import (
	"context"
	"errors"
	"testing"

	"oc-manager/internal/audit"
	"oc-manager/internal/service"
)

// fakeAuditRecorder 用于断言 RecordNewAPIFailure 实际写入的事件字段。
type fakeAuditRecorder struct {
	events []service.AuditEvent
}

func (f *fakeAuditRecorder) Record(ctx context.Context, event service.AuditEvent) (service.AuditResult, error) {
	f.events = append(f.events, event)
	return service.AuditResult{}, nil
}

func TestRecordNewAPIFailure_WritesAuditEvent(t *testing.T) {
	rec := &fakeAuditRecorder{}
	h := audit.NewNewAPIAuditHelper(rec)

	err := errors.New("status=500 调用 new-api /api/user/ 失败")
	h.RecordFailure(context.Background(), audit.NewAPIFailureContext{
		ActorID:   "00000000-0000-0000-0000-000000000001",
		ActorRole: "platform_admin",
		OrgID:     "00000000-0000-0000-0000-000000000099",
		Endpoint:  "POST /api/user/",
		Status:    500,
		Err:       err,
	})

	if len(rec.events) != 1 {
		t.Fatalf("期望 1 条事件，实际 %d", len(rec.events))
	}
	e := rec.events[0]
	if e.TargetType != "newapi_call" {
		t.Errorf("TargetType=%q，期望 newapi_call", e.TargetType)
	}
	if e.Result != "failed" {
		t.Errorf("Result=%q，期望 failed", e.Result)
	}
	if e.Action != "POST /api/user/" {
		t.Errorf("Action=%q，期望 POST /api/user/", e.Action)
	}
	if e.ActorRole != "platform_admin" {
		t.Errorf("ActorRole=%q", e.ActorRole)
	}
	if e.Metadata["status_code"] != 500 {
		t.Errorf("metadata.status_code=%v", e.Metadata["status_code"])
	}
	if e.ErrorMessage == "" {
		t.Errorf("ErrorMessage 不应为空")
	}
}

func TestRecordNewAPIFailure_NoActorContextDefaultsToSystem(t *testing.T) {
	rec := &fakeAuditRecorder{}
	h := audit.NewNewAPIAuditHelper(rec)

	h.RecordFailure(context.Background(), audit.NewAPIFailureContext{
		// 不传 ActorID / ActorRole（worker 后台路径）
		Endpoint: "POST /api/token/",
		Status:   500,
		Err:      errors.New("connection refused"),
	})

	if len(rec.events) != 1 {
		t.Fatalf("期望 1 条事件，实际 %d", len(rec.events))
	}
	if rec.events[0].ActorRole != "system" {
		t.Errorf("ActorRole=%q，期望 system（worker 默认）", rec.events[0].ActorRole)
	}
	if rec.events[0].ActorID != "" {
		t.Errorf("ActorID 应为空，实际 %q", rec.events[0].ActorID)
	}
}

func TestRecordNewAPIFailure_RecorderErrorDoesNotPanic(t *testing.T) {
	rec := &erroringAuditRecorder{}
	h := audit.NewNewAPIAuditHelper(rec)

	// 即使底层 Record 返回错误，helper 自己不应 panic 或 propagate
	h.RecordFailure(context.Background(), audit.NewAPIFailureContext{
		Endpoint: "GET /api/log/",
		Status:   500,
		Err:      errors.New("upstream 5xx"),
	})
}

type erroringAuditRecorder struct{}

func (erroringAuditRecorder) Record(ctx context.Context, event service.AuditEvent) (service.AuditResult, error) {
	return service.AuditResult{}, errors.New("audit store down")
}
