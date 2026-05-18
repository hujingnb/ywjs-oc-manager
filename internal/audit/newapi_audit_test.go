package audit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestRecordNewAPIFailure_WritesAuditEvent 验证记录 new-api失败写入审计事件的成功路径场景。
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

	require.Equal(t, 1, len(rec.events))
	e := rec.events[0]
	assert.Equal(t, "newapi_call", e.TargetType)
	assert.Equal(t, "failed", e.Result)
	assert.Equal(t, "POST /api/user/", e.Action)
	assert.Equal(t, "platform_admin", e.ActorRole)
	assert.Equal(t, 500, e.Metadata["status_code"])
	assert.NotEqual(t, "", e.ErrorMessage)
	// 详情字段应展示「HTTP <status>」便于审计列表识别失败码。
	assert.Equal(t, "HTTP 500", e.DetailMessage)
}

// TestRecordNewAPIFailure_NoActorContextDefaultsToSystem 验证记录 new-api失败无操作者上下文默认值到系统的边界条件场景。
func TestRecordNewAPIFailure_NoActorContextDefaultsToSystem(t *testing.T) {
	rec := &fakeAuditRecorder{}
	h := audit.NewNewAPIAuditHelper(rec)

	h.RecordFailure(context.Background(), audit.NewAPIFailureContext{
		// 不传 ActorID / ActorRole（worker 后台路径）
		Endpoint: "POST /api/token/",
		Status:   500,
		Err:      errors.New("connection refused"),
	})

	require.Equal(t, 1, len(rec.events))
	assert.Equal(t, "system", rec.events[0].ActorRole)
	assert.Equal(t, "", rec.events[0].ActorID)
	// 即使没有 actor，HTTP 状态依然展示。
	assert.Equal(t, "HTTP 500", rec.events[0].DetailMessage)
}

// TestRecordNewAPIFailure_RecorderErrorDoesNotPanic 验证记录 new-api失败记录器错误不会 panic的预期行为场景。
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
