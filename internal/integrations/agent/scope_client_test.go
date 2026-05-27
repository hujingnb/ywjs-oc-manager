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

// TestScopeClient_UploadAppInputFile 验证 UploadAppInputFile 走 PUT
// /v1/scopes/apps/<id>/input/file?path=<relPath>，并透传 body 与 Authorization 头。
// 该端点对应 agent T13 新增的 input/file 路由，与旧 runtime/file 路由不同；
// 用例覆盖正常路径，确保 manager 端不会再误发到已下线的 runtime/file。
func TestScopeClient_UploadAppInputFile(t *testing.T) {
	s := newScopeServer(nil)
	defer s.Close()
	c := NewFileClient(s.URL, "tok")

	// 场景：上传 manifest.yaml 到 apps/app-1/input/manifest.yaml，
	// 验证 method=PUT、URL 路径、query 内 relPath 经 url-encode、body 与 auth 头一致。
	err := c.UploadAppInputFile(context.Background(), "app-1", "manifest.yaml",
		strings.NewReader("yaml-body"))
	require.NoError(t, err)
	require.Len(t, s.captured, 1)
	got := s.captured[0]
	require.Equal(t, http.MethodPut, got.method)
	require.Equal(t, "/v1/scopes/apps/app-1/input/file", got.path)
	require.Equal(t, "path=manifest.yaml", got.query)
	require.Equal(t, "yaml-body", string(got.body))
	require.Equal(t, "Bearer tok", got.auth)
	require.Equal(t, "application/octet-stream", got.contType)
}

// TestScopeClient_UploadAppInputFile_EncodesRelPath 验证含子目录的 relPath
// 在 query 中按 url 标准编码，不会被 / 字符提前截断 path。
func TestScopeClient_UploadAppInputFile_EncodesRelPath(t *testing.T) {
	s := newScopeServer(nil)
	defer s.Close()
	c := NewFileClient(s.URL, "tok")

	// 场景：写 resources/persona.md 这种带子目录的 relPath，
	// 验证 query 编码后 / 变成 %2F，server 侧仍能拿到完整 relPath。
	err := c.UploadAppInputFile(context.Background(), "app-1", "resources/persona.md",
		strings.NewReader("persona"))
	require.NoError(t, err)
	require.Len(t, s.captured, 1)
	require.Equal(t, "path=resources%2Fpersona.md", s.captured[0].query)
}

// TestScopeClient_UploadAppInputFile_PropagatesErrorBody 验证 agent 返回 4xx 时
// 错误体能被透传到 manager 调用方，便于上层定位 sandbox 越界等问题。
func TestScopeClient_UploadAppInputFile_PropagatesErrorBody(t *testing.T) {
	s := newScopeServer(func(req capturedReq, w http.ResponseWriter) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"path escapes input sandbox"}`))
	})
	defer s.Close()
	c := NewFileClient(s.URL, "tok")

	// 场景：agent 拒绝越界 relPath，返回 403 + 错误说明；
	// 验证 UploadAppInputFile 返回的 err.Error() 包含 agent 错误描述。
	err := c.UploadAppInputFile(context.Background(), "app-1", "../escape",
		strings.NewReader("x"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "path escapes input sandbox")
}

// TestScopeClient_DeleteAppInputFile 验证 DeleteAppInputFile 走 HTTP DELETE
// /v1/scopes/apps/<id>/input/file?path=<relPath>，与 UploadAppInputFile 共用同一路由,
// 仅 HTTP method 不同。
func TestScopeClient_DeleteAppInputFile(t *testing.T) {
	s := newScopeServer(nil)
	defer s.Close()
	c := NewFileClient(s.URL, "tok")

	// 场景:删除 apps/app-1/input/resources/config/policy.md,
	// 验证 method=DELETE、path、query 编码、Authorization 头是否就位。
	err := c.DeleteAppInputFile(context.Background(), "app-1", "resources/config/policy.md")
	require.NoError(t, err)
	require.Len(t, s.captured, 1)
	got := s.captured[0]
	require.Equal(t, http.MethodDelete, got.method)
	require.Equal(t, "/v1/scopes/apps/app-1/input/file", got.path)
	require.Equal(t, "path=resources%2Fconfig%2Fpolicy.md", got.query)
	require.Equal(t, "Bearer tok", got.auth)
}

// TestScopeClient_DeleteAppInputFile_RejectsEmpty 验证 relPath 为空时
// 不发请求即返回错误, 防止 manager 误删整个 input 目录。
func TestScopeClient_DeleteAppInputFile_RejectsEmpty(t *testing.T) {
	c := NewFileClient("http://nowhere", "tok")
	err := c.DeleteAppInputFile(context.Background(), "app-1", "")
	require.Error(t, err)
}

// TestAppScopedFileClient_WriteAppInputFile 验证 wrapper 能把
// hermes.AppInputWriter.WriteAppInputFile 调用正确转发到底层
// AgentFileClient.UploadAppInputFile，参数顺序保持一致 (appID, relPath, body)。
func TestAppScopedFileClient_WriteAppInputFile(t *testing.T) {
	s := newScopeServer(nil)
	defer s.Close()
	inner := NewFileClient(s.URL, "tok")
	wrapper := NewAppScopedFileClient(inner)

	// 场景：通过 wrapper 写 resources/rules.md，wrapper 不应吞掉参数 / 改写路径，
	// 验证最终 HTTP 请求与直接调 UploadAppInputFile 等价。
	err := wrapper.WriteAppInputFile(context.Background(), "app-2", "resources/rules.md",
		strings.NewReader("rule"))
	require.NoError(t, err)
	require.Len(t, s.captured, 1)
	got := s.captured[0]
	require.Equal(t, http.MethodPut, got.method)
	require.Equal(t, "/v1/scopes/apps/app-2/input/file", got.path)
	require.Equal(t, "path=resources%2Frules.md", got.query)
	require.Equal(t, "rule", string(got.body))
}
