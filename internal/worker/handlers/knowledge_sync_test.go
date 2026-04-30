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

type memoryKnowledgeSink struct {
	uploads []struct {
		node, path string
		body       string
	}
	deletes []struct {
		node, path string
	}
	uploadErr error
	deleteErr error
}

func (s *memoryKnowledgeSink) UploadFile(_ context.Context, nodeID, remotePath string, content io.Reader) error {
	if s.uploadErr != nil {
		return s.uploadErr
	}
	body, _ := io.ReadAll(content)
	s.uploads = append(s.uploads, struct {
		node, path string
		body       string
	}{nodeID, remotePath, string(body)})
	return nil
}

func (s *memoryKnowledgeSink) DeletePath(_ context.Context, nodeID, remotePath string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deletes = append(s.deletes, struct {
		node, path string
	}{nodeID, remotePath})
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
	if err := handler.Handle(context.Background(), buildKnowledgeJob(t, payload)); err != nil {
		t.Fatalf("Handle err = %v", err)
	}
	if len(sink.uploads) != 1 {
		t.Fatalf("uploads = %d", len(sink.uploads))
	}
	got := sink.uploads[0]
	if got.node != "n1" || got.path != "orgs/o1/knowledge/notes/a.md" || got.body != "# hello" {
		t.Fatalf("upload = %+v", got)
	}
}

func TestKnowledgeSyncHandler_DeleteAppFile(t *testing.T) {
	sink := &memoryKnowledgeSink{}
	handler := NewKnowledgeSyncHandler(nil, sink)
	payload := []byte(`{"scope":"app","app_id":"a1","node_id":"n2","change_type":"delete_file","rel_path":"docs/x.md"}`)
	if err := handler.Handle(context.Background(), buildKnowledgeJob(t, payload)); err != nil {
		t.Fatalf("Handle err = %v", err)
	}
	if len(sink.deletes) != 1 {
		t.Fatalf("deletes = %+v", sink.deletes)
	}
	got := sink.deletes[0]
	if got.node != "n2" || got.path != "apps/a1/knowledge/docs/x.md" {
		t.Fatalf("delete = %+v", got)
	}
}

func TestKnowledgeSyncHandler_RejectsUnknownChangeType(t *testing.T) {
	handler := NewKnowledgeSyncHandler(nil, &memoryKnowledgeSink{})
	payload := []byte(`{"scope":"org","org_id":"o","node_id":"n","change_type":"boom","rel_path":""}`)
	err := handler.Handle(context.Background(), buildKnowledgeJob(t, payload))
	if err == nil || !strings.Contains(err.Error(), "未知 change_type") {
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
	if err := handler.Handle(context.Background(), buildKnowledgeJob(t, payload)); err == nil {
		t.Fatal("缺 node_id 应当报错")
	}
}
