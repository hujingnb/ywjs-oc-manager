package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

type memoryKnowledgeSource struct {
	files map[string][]byte
}

func (m *memoryKnowledgeSource) Open(p string) (io.ReadCloser, int64, error) {
	data, ok := m.files[p]
	if !ok {
		return nil, 0, errors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
}

// memoryKnowledgeSink 是 KnowledgeFileSink 的内存实现，用于断言 worker 调对了哪个 scope
// 的方法 + 哪个 scopeID + relPath。
type memoryKnowledgeSink struct {
	uploads   []sinkCall
	deletes   []sinkCall
	uploadErr error
	deleteErr error
}

type sinkCall struct {
	scope, node, scopeID, relPath, body string
}

func (s *memoryKnowledgeSink) UploadOrgFile(_ context.Context, nodeID, orgID, relPath string, content io.Reader) error {
	if s.uploadErr != nil {
		return s.uploadErr
	}
	body, _ := io.ReadAll(content)
	s.uploads = append(s.uploads, sinkCall{scope: "org", node: nodeID, scopeID: orgID, relPath: relPath, body: string(body)})
	return nil
}

func (s *memoryKnowledgeSink) UploadAppFile(_ context.Context, nodeID, appID, relPath string, content io.Reader) error {
	if s.uploadErr != nil {
		return s.uploadErr
	}
	body, _ := io.ReadAll(content)
	s.uploads = append(s.uploads, sinkCall{scope: "app", node: nodeID, scopeID: appID, relPath: relPath, body: string(body)})
	return nil
}

func (s *memoryKnowledgeSink) DeleteOrgFile(_ context.Context, nodeID, orgID, relPath string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deletes = append(s.deletes, sinkCall{scope: "org", node: nodeID, scopeID: orgID, relPath: relPath})
	return nil
}

func (s *memoryKnowledgeSink) DeleteAppFile(_ context.Context, nodeID, appID, relPath string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deletes = append(s.deletes, sinkCall{scope: "app", node: nodeID, scopeID: appID, relPath: relPath})
	return nil
}

func buildKnowledgeJob(t *testing.T, payload []byte) sqlc.Job {
	t.Helper()
	return sqlc.Job{Type: domain.JobTypeKnowledgeSyncNode, PayloadJson: payload}
}

// TestKnowledgeSyncHandler_UploadOrgFile 验证知识库同步处理器上传组织文件的预期行为场景。
func TestKnowledgeSyncHandler_UploadOrgFile(t *testing.T) {
	source := &memoryKnowledgeSource{files: map[string][]byte{
		"org/o1/knowledge/notes/a.md": []byte("# hello"),
	}}
	sink := &memoryKnowledgeSink{}
	handler := NewKnowledgeSyncHandler(source, sink)
	payload := []byte(`{"scope":"org","org_id":"o1","node_id":"n1","change_type":"upload_file","rel_path":"notes/a.md","master_path":"org/o1/knowledge/notes/a.md"}`)
	err := handler.Handle(context.Background(), buildKnowledgeJob(t, payload))
	require.NoError(t, err)
	require.Equal(t, 1, len(sink.uploads))
	got := sink.uploads[0]
	if got.scope != "org" || got.node != "n1" || got.scopeID != "o1" || got.relPath != "notes/a.md" || got.body != "# hello" {
		t.Fatalf("upload = %+v", got)
	}
}

// TestKnowledgeSyncHandler_DeleteAppFile 验证知识库同步处理器删除应用文件的预期行为场景。
func TestKnowledgeSyncHandler_DeleteAppFile(t *testing.T) {
	sink := &memoryKnowledgeSink{}
	handler := NewKnowledgeSyncHandler(nil, sink)
	payload := []byte(`{"scope":"app","app_id":"a1","node_id":"n2","change_type":"delete_file","rel_path":"docs/x.md"}`)
	err := handler.Handle(context.Background(), buildKnowledgeJob(t, payload))
	require.NoError(t, err)
	require.Equal(t, 1, len(sink.deletes))
	got := sink.deletes[0]
	if got.scope != "app" || got.node != "n2" || got.scopeID != "a1" || got.relPath != "docs/x.md" {
		t.Fatalf("delete = %+v", got)
	}
}

// TestKnowledgeSyncHandler_RejectsUnknownChangeType 验证知识库同步处理器拒绝未知变更类型的异常或拒绝路径场景。
func TestKnowledgeSyncHandler_RejectsUnknownChangeType(t *testing.T) {
	handler := NewKnowledgeSyncHandler(nil, &memoryKnowledgeSink{})
	payload := []byte(`{"scope":"org","org_id":"o","node_id":"n","change_type":"boom","rel_path":"x"}`)
	err := handler.Handle(context.Background(), buildKnowledgeJob(t, payload))
	if err == nil || !strings.Contains(err.Error(), "未知 change_type") {
		t.Fatalf("err = %v", err)
	}
}

// TestKnowledgeSyncHandler_RejectsMissingRelPath 验证知识库同步处理器拒绝缺失相对路径路径的异常或拒绝路径场景。
func TestKnowledgeSyncHandler_RejectsMissingRelPath(t *testing.T) {
	handler := NewKnowledgeSyncHandler(nil, &memoryKnowledgeSink{})
	payload := []byte(`{"scope":"org","org_id":"o","node_id":"n","change_type":"upload_file","rel_path":""}`)
	err := handler.Handle(context.Background(), buildKnowledgeJob(t, payload))
	if err == nil || !strings.Contains(err.Error(), "缺少 rel_path") {
		t.Fatalf("err = %v", err)
	}
}

// TestKnowledgeSyncHandler_PropagatesUploadError 验证知识库同步处理器透传上传错误的错误映射或错误记录场景。
func TestKnowledgeSyncHandler_PropagatesUploadError(t *testing.T) {
	source := &memoryKnowledgeSource{files: map[string][]byte{"x": []byte("ok")}}
	sink := &memoryKnowledgeSink{uploadErr: errors.New("agent unreachable")}
	handler := NewKnowledgeSyncHandler(source, sink)
	payload := []byte(`{"scope":"org","org_id":"o","node_id":"n","change_type":"upload_file","rel_path":"y","master_path":"x"}`)
	err := handler.Handle(context.Background(), buildKnowledgeJob(t, payload))
	if err == nil || !strings.Contains(err.Error(), "上传到节点失败") {
		t.Fatalf("err = %v", err)
	}
}

// TestKnowledgeSyncHandler_RejectsMissingNodeID 验证知识库同步处理器拒绝缺失节点ID的异常或拒绝路径场景。
func TestKnowledgeSyncHandler_RejectsMissingNodeID(t *testing.T) {
	handler := NewKnowledgeSyncHandler(nil, &memoryKnowledgeSink{})
	payload := []byte(`{"scope":"org","org_id":"o","change_type":"delete_file","rel_path":"x"}`)
	err := handler.Handle(context.Background(), buildKnowledgeJob(t, payload))
	require.Error(t, err)
}

// TestKnowledgeSyncHandler_AppScopeSuccessWritesAudit 验证 app scope 同步成功时
// 通过注入的 auditor 写一条 result=succeeded 的 audit_logs 事件,target_type
// 必须为 app_knowledge_sync,target_id 取 app_id,让审计页面可按应用筛选。
func TestKnowledgeSyncHandler_AppScopeSuccessWritesAudit(t *testing.T) {
	source := &memoryKnowledgeSource{files: map[string][]byte{"org/o1/app/a1/knowledge/x.md": []byte("body")}}
	sink := &memoryKnowledgeSink{}
	auditor := &fakeWorkerAuditor{}
	handler := NewKnowledgeSyncHandler(source, sink)
	handler.SetAuditor(auditor)

	payload := []byte(`{"scope":"app","org_id":"o1","app_id":"a1","node_id":"n1","change_type":"upload_file","rel_path":"x.md","master_path":"org/o1/app/a1/knowledge/x.md"}`)
	require.NoError(t, handler.Handle(context.Background(), buildKnowledgeJob(t, payload)))

	require.Len(t, auditor.events, 1)
	ev := auditor.events[0]
	assert.Equal(t, "app_knowledge_sync", ev.TargetType)
	assert.Equal(t, "a1", ev.TargetID)
	assert.Equal(t, "upload_file", ev.Action)
	assert.Equal(t, "succeeded", ev.Result)
	assert.Equal(t, "n1", ev.Metadata["node_id"])
	assert.Equal(t, "x.md", ev.Metadata["rel_path"])
}

// TestKnowledgeSyncHandler_AppScopeFailureWritesAudit 验证失败时 result=failed
// 且 error_message 非空,handler 不吞错(仍返回原 sink 错误)。
func TestKnowledgeSyncHandler_AppScopeFailureWritesAudit(t *testing.T) {
	source := &memoryKnowledgeSource{files: map[string][]byte{"m": []byte("x")}}
	sink := &memoryKnowledgeSink{uploadErr: errors.New("agent unreachable")}
	auditor := &fakeWorkerAuditor{}
	handler := NewKnowledgeSyncHandler(source, sink)
	handler.SetAuditor(auditor)

	payload := []byte(`{"scope":"app","org_id":"o","app_id":"a","node_id":"n","change_type":"upload_file","rel_path":"r","master_path":"m"}`)
	err := handler.Handle(context.Background(), buildKnowledgeJob(t, payload))
	require.Error(t, err)
	require.Len(t, auditor.events, 1)
	assert.Equal(t, "failed", auditor.events[0].Result)
	assert.Contains(t, auditor.events[0].ErrorMessage, "agent unreachable")
}

// TestKnowledgeSyncHandler_OrgScopeDoesNotWriteAudit 验证 org scope 不走 audit_logs
// (已经有 knowledge_sync_status 表),避免双写造成事件冗余。
func TestKnowledgeSyncHandler_OrgScopeDoesNotWriteAudit(t *testing.T) {
	source := &memoryKnowledgeSource{files: map[string][]byte{"m": []byte("x")}}
	sink := &memoryKnowledgeSink{}
	auditor := &fakeWorkerAuditor{}
	handler := NewKnowledgeSyncHandler(source, sink)
	handler.SetAuditor(auditor)

	payload := []byte(`{"scope":"org","org_id":"o1","node_id":"n1","change_type":"upload_file","rel_path":"r","master_path":"m"}`)
	require.NoError(t, handler.Handle(context.Background(), buildKnowledgeJob(t, payload)))
	require.Empty(t, auditor.events) // org scope 必须由 knowledge_sync_status 表承担,不进 audit_logs。
}

// fakeWorkerAuditor 收集 worker handler 提交的 AuditEvent,供断言。
type fakeWorkerAuditor struct {
	events []service.AuditEvent
}

func (f *fakeWorkerAuditor) Record(_ context.Context, event service.AuditEvent) (service.AuditResult, error) {
	f.events = append(f.events, event)
	return service.AuditResult{}, nil
}
