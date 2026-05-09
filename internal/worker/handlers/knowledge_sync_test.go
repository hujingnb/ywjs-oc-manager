package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
	"github.com/stretchr/testify/require"
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
	uploads []sinkCall
	deletes []sinkCall
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

func TestKnowledgeSyncHandler_RejectsUnknownChangeType(t *testing.T) {
	handler := NewKnowledgeSyncHandler(nil, &memoryKnowledgeSink{})
	payload := []byte(`{"scope":"org","org_id":"o","node_id":"n","change_type":"boom","rel_path":"x"}`)
	err := handler.Handle(context.Background(), buildKnowledgeJob(t, payload))
	if err == nil || !strings.Contains(err.Error(), "未知 change_type") {
		t.Fatalf("err = %v", err)
	}
}

func TestKnowledgeSyncHandler_RejectsMissingRelPath(t *testing.T) {
	handler := NewKnowledgeSyncHandler(nil, &memoryKnowledgeSink{})
	payload := []byte(`{"scope":"org","org_id":"o","node_id":"n","change_type":"upload_file","rel_path":""}`)
	err := handler.Handle(context.Background(), buildKnowledgeJob(t, payload))
	if err == nil || !strings.Contains(err.Error(), "缺少 rel_path") {
		t.Fatalf("err = %v", err)
	}
}

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

func TestKnowledgeSyncHandler_RejectsMissingNodeID(t *testing.T) {
	handler := NewKnowledgeSyncHandler(nil, &memoryKnowledgeSink{})
	payload := []byte(`{"scope":"org","org_id":"o","change_type":"delete_file","rel_path":"x"}`)
	err := handler.Handle(context.Background(), buildKnowledgeJob(t, payload))
	require.Error(t, err)
}
