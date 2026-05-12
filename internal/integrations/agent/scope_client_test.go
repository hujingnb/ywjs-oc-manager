package agent

import (
	"bytes"
	"context"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// scopeServer 把每条捕获到的请求记录下来，便于断言路径 / 方法 / body。
type scopeServer struct {
	*httptest.Server
	captured []capturedReq
}

type capturedReq struct {
	method   string
	path     string
	query    string
	body     []byte
	auth     string
	contType string
}

// newScopeServer 启动一个 mock agent，所有请求都返回 200 + provided body（默认空）。
func newScopeServer(handler func(req capturedReq, w http.ResponseWriter)) *scopeServer {
	s := &scopeServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		req := capturedReq{
			method:   r.Method,
			path:     r.URL.Path,
			query:    r.URL.RawQuery,
			body:     body,
			auth:     r.Header.Get("Authorization"),
			contType: r.Header.Get("Content-Type"),
		}
		s.captured = append(s.captured, req)
		if handler != nil {
			handler(req, w)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	return s
}

// TestScopeClient_InitAppDirs 验证scope客户端初始化应用目录的预期行为场景。
func TestScopeClient_InitAppDirs(t *testing.T) {
	s := newScopeServer(nil)
	defer s.Close()
	c := NewFileClient(s.URL, "agent-tok")
	err := c.InitAppDirs(context.Background(), "app-1")
	require.NoError(t, err)
	require.Equal(t, 1, len(s.captured))
	got := s.captured[0]
	require.Equal(t, http.MethodPost, got.method)
	require.Equal(t, "/v1/scopes/apps/app-1/init", got.path)
	require.Equal(t, "Bearer agent-tok", got.auth)
}

// TestScopeClient_SyncOrgKnowledge 验证scope客户端同步组织知识库的预期行为场景。
func TestScopeClient_SyncOrgKnowledge(t *testing.T) {
	s := newScopeServer(nil)
	defer s.Close()
	c := NewFileClient(s.URL, "tok")

	body := bytes.NewReader([]byte("fake-tar-bytes"))
	err := c.SyncOrgKnowledge(context.Background(), "org-7", body)
	require.NoError(t, err)
	got := s.captured[0]
	require.Equal(t, "/v1/scopes/orgs/org-7/knowledge/sync", got.path)
	require.Equal(t, "application/x-tar", got.contType)
	require.Equal(t, "fake-tar-bytes", string(got.body))
}

// TestScopeClient_SyncAppKnowledge 验证scope客户端同步应用知识库的预期行为场景。
func TestScopeClient_SyncAppKnowledge(t *testing.T) {
	s := newScopeServer(nil)
	defer s.Close()
	c := NewFileClient(s.URL, "tok")
	err := c.SyncAppKnowledge(context.Background(), "app-1", strings.NewReader("x"))
	require.NoError(t, err)
	got := s.captured[0]
	require.Equal(t, "/v1/scopes/apps/app-1/knowledge/sync", got.path)
}

// TestScopeClient_KnowledgeFile_Upload_Delete 验证scope客户端知识库文件上传删除的预期行为场景。
func TestScopeClient_KnowledgeFile_Upload_Delete(t *testing.T) {
	s := newScopeServer(nil)
	defer s.Close()
	c := NewFileClient(s.URL, "tok")

	if err := c.UploadAppKnowledgeFile(context.Background(), "app-1", "sub/note.txt",
		strings.NewReader("hello")); err != nil {
		t.Fatalf("upload err=%v", err)
	}
	if err := c.UploadOrgKnowledgeFile(context.Background(), "org-1", "policy.md",
		strings.NewReader("policy")); err != nil {
		t.Fatalf("upload org err=%v", err)
	}
	err := c.DeleteAppKnowledge(context.Background(), "app-1", "sub/note.txt")
	require.NoError(t, err)
	err = c.DeleteOrgKnowledge(context.Background(), "org-1", "policy.md")
	require.NoError(t, err)

	require.Equal(t, 4, len(s.captured))
	expects := []struct {
		method, path string
		query        string
	}{
		{http.MethodPut, "/v1/scopes/apps/app-1/knowledge/file", "path=sub%2Fnote.txt"},    // 场景：应用知识库上传应调用 app scope 的 PUT file 端点并编码相对路径
		{http.MethodPut, "/v1/scopes/orgs/org-1/knowledge/file", "path=policy.md"},         // 场景：组织知识库上传应调用 org scope 的 PUT file 端点
		{http.MethodDelete, "/v1/scopes/apps/app-1/knowledge/file", "path=sub%2Fnote.txt"}, // 场景：应用知识库删除应调用 app scope 的 DELETE file 端点并编码相对路径
		{http.MethodDelete, "/v1/scopes/orgs/org-1/knowledge/file", "path=policy.md"},      // 场景：组织知识库删除应调用 org scope 的 DELETE file 端点
	}
	for i, want := range expects {
		got := s.captured[i]
		if got.method != want.method || got.path != want.path || got.query != want.query {
			t.Fatalf("call#%d: %+v != %+v", i, got, want)
		}
	}
}

// TestScopeClient_KnowledgeFile_RejectsEmptyRel 验证scope客户端知识库文件拒绝空值相对路径的异常或拒绝路径场景。
func TestScopeClient_KnowledgeFile_RejectsEmptyRel(t *testing.T) {
	c := NewFileClient("http://nowhere", "tok")
	err := c.UploadAppKnowledgeFile(context.Background(), "app-1", "", strings.NewReader("x"))
	require.Error(t, err)
	err = c.DeleteAppKnowledge(context.Background(), "app-1", "")
	require.Error(t, err)
}

// TestScopeClient_ListWorkspace 验证scope客户端列表工作区的预期行为场景。
func TestScopeClient_ListWorkspace(t *testing.T) {
	s := newScopeServer(func(req capturedReq, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"path":"/sub","entries":[
			{"name":"a.txt","type":"file","size":3,"modified_at":"2026-05-02T12:00:00Z"},
			{"name":"sub2","type":"dir","modified_at":"2026-05-02T12:00:00Z"}
		]}`))
	})
	defer s.Close()
	c := NewFileClient(s.URL, "tok")
	listing, err := c.ListWorkspace(context.Background(), "app-1", "sub")
	require.NoError(t, err)
	if listing.Path != "/sub" || len(listing.Entries) != 2 {
		t.Fatalf("listing=%+v", listing)
	}
	if listing.Entries[0].Name != "a.txt" || listing.Entries[0].Type != "file" || listing.Entries[0].Size != 3 {
		t.Fatalf("e0=%+v", listing.Entries[0])
	}
	if listing.Entries[1].Name != "sub2" || listing.Entries[1].Type != "dir" {
		t.Fatalf("e1=%+v", listing.Entries[1])
	}
	got := s.captured[0]
	if got.path != "/v1/scopes/apps/app-1/workspace" || got.query != "path=sub" {
		t.Fatalf("query=%s path=%s", got.query, got.path)
	}
}

// TestScopeClient_ListWorkspace_RootNoPath 验证scope客户端列表工作区根目录无路径的预期行为场景。
func TestScopeClient_ListWorkspace_RootNoPath(t *testing.T) {
	s := newScopeServer(func(req capturedReq, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"path":"/","entries":[]}`))
	})
	defer s.Close()
	c := NewFileClient(s.URL, "tok")
	_, err := c.ListWorkspace(context.Background(), "app-1", "")
	require.NoError(t, err)
	require.Equal(t, "", s.captured[0].query)
}

// TestScopeClient_DownloadWorkspaceFile 验证scope客户端下载工作区文件的预期行为场景。
func TestScopeClient_DownloadWorkspaceFile(t *testing.T) {
	s := newScopeServer(func(req capturedReq, w http.ResponseWriter) {
		_, _ = w.Write([]byte("file-bytes"))
	})
	defer s.Close()
	c := NewFileClient(s.URL, "tok")
	rc, err := c.DownloadWorkspaceFile(context.Background(), "app-1", "out.txt")
	require.NoError(t, err)
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	require.Equal(t, "file-bytes", string(body))
	require.Equal(t, "/v1/scopes/apps/app-1/workspace/download", s.captured[0].path)
}

// TestScopeClient_DownloadWorkspaceFile_RejectsEmpty 验证scope客户端下载工作区文件拒绝空值的异常或拒绝路径场景。
func TestScopeClient_DownloadWorkspaceFile_RejectsEmpty(t *testing.T) {
	c := NewFileClient("http://nowhere", "tok")
	_, err := c.DownloadWorkspaceFile(context.Background(), "app-1", "")
	require.Error(t, err)
}

// TestScopeClient_StreamWorkspaceArchive 验证scope客户端流工作区归档的预期行为场景。
func TestScopeClient_StreamWorkspaceArchive(t *testing.T) {
	s := newScopeServer(func(req capturedReq, w http.ResponseWriter) {
		_, _ = w.Write([]byte("zip-bytes"))
	})
	defer s.Close()
	c := NewFileClient(s.URL, "tok")
	var buf bytes.Buffer
	err := c.StreamWorkspaceArchive(context.Background(), "app-1", "sub", &buf)
	require.NoError(t, err)
	require.Equal(t, "zip-bytes", buf.String())
	got := s.captured[0]
	if got.path != "/v1/scopes/apps/app-1/workspace/archive" || got.query != "path=sub" {
		t.Fatalf("got=%+v", got)
	}
}

// TestScopeClient_ArchiveApp 验证scope客户端归档应用的预期行为场景。
func TestScopeClient_ArchiveApp(t *testing.T) {
	s := newScopeServer(nil)
	defer s.Close()
	c := NewFileClient(s.URL, "tok")
	err := c.ArchiveApp(context.Background(), "app-1")
	require.NoError(t, err)
	got := s.captured[0]
	if got.method != http.MethodPost || got.path != "/v1/scopes/apps/app-1/archive" {
		t.Fatalf("got=%+v", got)
	}
}

// TestScopeClient_CleanupArchive 验证scope客户端清理归档的预期行为场景。
func TestScopeClient_CleanupArchive(t *testing.T) {
	s := newScopeServer(nil)
	defer s.Close()
	c := NewFileClient(s.URL, "tok")
	err := c.CleanupArchive(context.Background(), 30)
	require.NoError(t, err)
	got := s.captured[0]
	if got.path != "/v1/scopes/cleanup-archives" || got.query != "retention_days=30" {
		t.Fatalf("got=%+v", got)
	}

	err = c.CleanupArchive(context.Background(), 0)
	require.Error(t, err)
}

// TestScopeClient_PropagatesErrorBody 验证scope客户端透传错误请求体的错误映射或错误记录场景。
func TestScopeClient_PropagatesErrorBody(t *testing.T) {
	s := newScopeServer(func(req capturedReq, w http.ResponseWriter) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid path"}`))
	})
	defer s.Close()
	c := NewFileClient(s.URL, "tok")
	err := c.InitAppDirs(context.Background(), "app-1")
	if err == nil || !strings.Contains(err.Error(), "invalid path") {
		t.Fatalf("err 应携带 agent 返回的错误体: %v", err)
	}
}
