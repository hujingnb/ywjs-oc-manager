package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// jsonDecoder 与 readAll 是测试辅助 helper（避免重复 import）。
func jsonDecoder(r io.Reader) *json.Decoder { return json.NewDecoder(r) }
func readAll(r io.Reader) ([]byte, error)   { return io.ReadAll(r) }

// makeTar 把 (path → content) 打成一个 tar 流供测试用。
func makeTar(t *testing.T, files map[string]string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg,
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(content)),
		}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		_, err := tw.Write([]byte(content))
		require.NoError(t, err)
	}
	err := tw.Close()
	require.NoError(t, err)
	return &buf
}

// TestResolveScopePath 验证解析scope路径的预期行为场景。
func TestResolveScopePath(t *testing.T) {
	dataRoot := t.TempDir()
	scope := "apps/abc"

	cases := []struct {
		name    string
		rel     string
		wantErr bool
	}{
		{"empty rel returns scope root", "", false},                       // 场景：空相对路径应解析到 scope 根目录
		{"slash returns scope root", "/", false},                          // 场景：单斜杠路径应解析到 scope 根目录
		{"clean nested file", "workspace/foo.txt", false},                 // 场景：普通嵌套文件路径应被接受
		{"deep clean path", "knowledge/sub/dir/file.pdf", false},          // 场景：更深层级的合法知识库路径应被接受
		{"rel with dot dot rejected", "../bbb", true},                     // 场景：显式上级目录路径应被拒绝
		{"abs path rejected", "/etc/passwd", true},                        // 场景：绝对路径应被拒绝
		{"hidden traversal rejected", "workspace/../../etc/passwd", true}, // 场景：隐藏在中间段的路径穿越应被拒绝
		{"trailing dot dot rejected", "workspace/..", true},               // 场景：以父级目录结尾的路径应被拒绝
		{"sibling escape rejected", "../../apps/other/workspace", true},   // 场景：试图跳到兄弟 scope 的路径应被拒绝
	}
	for _, c := range cases {
		// 当前子测试覆盖表格用例中该名称对应的输入组合、边界条件和期望结果。
		t.Run(c.name, func(t *testing.T) {
			abs, err := resolveScopePath(dataRoot, scope, c.rel)
			if c.wantErr {
				require.ErrorIs(t, err, ErrInvalidPath)
				return
			}
			require.NoError(t, err)
			scopeRoot, _ := filepath.Abs(filepath.Join(dataRoot, scope))
			if abs != scopeRoot && !strings.HasPrefix(abs+string(filepath.Separator), scopeRoot+string(filepath.Separator)) {
				t.Fatalf("abs=%s not under scopeRoot=%s", abs, scopeRoot)
			}
		})
	}
}

// TestScopesHandler_UnknownActionReturns404 验证scope处理器未知操作返回404的成功路径场景。
func TestScopesHandler_UnknownActionReturns404(t *testing.T) {
	srv := httptest.NewServer(newHandlerWithDocker(t.TempDir(), nil, "tok"))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/scopes/apps/abc/no-such-action", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestScopesAppInit_CreatesTwoDirs 验证新挂载布局下 app init 预建
// input/resources/knowledge/{org,app} 与 data/workspace 三层目录。
// data/workspace 必须预建:Hermes config.yaml 把 terminal.cwd 设为 workspace,
// 容器内首次 exec 命令前目录不存在会 cd 失败;manager workspace API 也读这个路径。
func TestScopesAppInit_CreatesTwoDirs(t *testing.T) {
	dataRoot := t.TempDir()
	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/scopes/apps/app-123/init", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	// 验证必须存在的 3 个子目录:input/resources/knowledge/org、
	// input/resources/knowledge/app 与 data/workspace。
	for _, sub := range []string{
		"input/resources/knowledge/org",
		"input/resources/knowledge/app",
		"data/workspace",
	} {
		dir := filepath.Join(dataRoot, "apps", "app-123", sub)
		fi, err := os.Stat(dir)
		require.NoError(t, err, "目录 %q 应被创建", sub)
		if !fi.IsDir() {
			t.Fatalf("%q not a directory", sub)
		}
	}
	// 验证旧布局的目录(OpenClaw / 老 Hermes 时代)不再被预建。
	for _, old := range []string{".hermes", "knowledge", "openclaw-config", "weixin", "workspace", "state", "logs"} {
		dir := filepath.Join(dataRoot, "apps", "app-123", old)
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("legacy 目录 %q 不应被预建，err=%v", old, err)
		}
	}
}

// TestScopesAppInit_Idempotent 验证scope 应用初始化幂等的特殊分支或幂等场景。
func TestScopesAppInit_Idempotent(t *testing.T) {
	dataRoot := t.TempDir()
	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()

	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/scopes/apps/app-123/init", nil)
		req.Header.Set("Authorization", "Bearer tok")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}
}

// TestScopesKnowledgeSync_App_ReplaceContents 验证scope 知识库同步应用替换内容的预期行为场景。
func TestScopesKnowledgeSync_App_ReplaceContents(t *testing.T) {
	dataRoot := t.TempDir()
	stale := filepath.Join(dataRoot, "apps", "app-1", "knowledge", "stale.txt")
	err := os.MkdirAll(filepath.Dir(stale), 0o755)
	require.NoError(t, err)
	err = os.WriteFile(stale, []byte("old"), 0o644)
	require.NoError(t, err)

	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()

	body := makeTar(t, map[string]string{
		"a.txt":     "hello",
		"sub/b.pdf": "fake-pdf",
	})
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/v1/scopes/apps/app-1/knowledge/sync", body)
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Content-Type", "application/x-tar")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale.txt 应被替换，err=%v", err)
	}
	got, err := os.ReadFile(filepath.Join(dataRoot, "apps", "app-1", "knowledge", "a.txt"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("a.txt = %q, %v", got, err)
	}
	got, err = os.ReadFile(filepath.Join(dataRoot, "apps", "app-1", "knowledge", "sub", "b.pdf"))
	if err != nil || string(got) != "fake-pdf" {
		t.Fatalf("b.pdf = %q, %v", got, err)
	}
}

// TestScopesKnowledgeSync_Org_CreatesPath 验证scope 知识库同步组织创建路径的成功路径场景。
func TestScopesKnowledgeSync_Org_CreatesPath(t *testing.T) {
	dataRoot := t.TempDir()
	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()

	body := makeTar(t, map[string]string{"intro.md": "# org"})
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/v1/scopes/orgs/org-1/knowledge/sync", body)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	got, err := os.ReadFile(filepath.Join(dataRoot, "orgs", "org-1", "knowledge", "intro.md"))
	if err != nil || string(got) != "# org" {
		t.Fatalf("intro.md = %q, %v", got, err)
	}
}

// TestScopesKnowledgeSync_RejectsTraversalEntry 验证scope 知识库同步拒绝路径穿越条目的异常或拒绝路径场景。
func TestScopesKnowledgeSync_RejectsTraversalEntry(t *testing.T) {
	dataRoot := t.TempDir()
	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()

	body := makeTar(t, map[string]string{"../../etc/passwd": "evil"})
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/v1/scopes/apps/app-1/knowledge/sync", body)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	_, err = os.Stat("/etc/passwd-evil")
	require.Error(t, err)
}

// TestScopesKnowledgeSync_EmptyTar 验证scope 知识库同步空 tar的边界条件场景。
func TestScopesKnowledgeSync_EmptyTar(t *testing.T) {
	dataRoot := t.TempDir()
	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()

	// 空 tar 流（合法 tar 末尾），sync 后 scope 目录存在但为空。
	body := makeTar(t, map[string]string{})
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/v1/scopes/apps/app-1/knowledge/sync", body)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	dir := filepath.Join(dataRoot, "apps", "app-1", "knowledge")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 0)
}

// TestHandleAppInputFile_PutWritesIntoInputSandbox 验证 PUT 写到 apps/<id>/input/
// sandbox 内部，且支持嵌套子路径、覆盖写入与幂等删除。
func TestHandleAppInputFile_PutWritesIntoInputSandbox(t *testing.T) {
	dataRoot := t.TempDir()
	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()

	// 嵌套子路径写入:resources/knowledge/app/note.txt 应落到 input 沙箱下。
	put, _ := http.NewRequest(http.MethodPut,
		srv.URL+"/v1/scopes/apps/app-1/input/file?path=resources/knowledge/app/note.txt",
		strings.NewReader("hello world"))
	put.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(put)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	dest := filepath.Join(dataRoot, "apps", "app-1", "input", "resources", "knowledge", "app", "note.txt")
	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	require.Equal(t, "hello world", string(got))

	// 覆盖写入(同名):验证 PUT 语义。
	put2, _ := http.NewRequest(http.MethodPut,
		srv.URL+"/v1/scopes/apps/app-1/input/file?path=resources/knowledge/app/note.txt",
		strings.NewReader("v2"))
	put2.Header.Set("Authorization", "Bearer tok")
	resp, _ = http.DefaultClient.Do(put2)
	resp.Body.Close()
	got, _ = os.ReadFile(dest)
	require.Equal(t, "v2", string(got))

	// 删除文件:验证 DELETE 真删了沙箱内文件。
	del, _ := http.NewRequest(http.MethodDelete,
		srv.URL+"/v1/scopes/apps/app-1/input/file?path=resources/knowledge/app/note.txt", nil)
	del.Header.Set("Authorization", "Bearer tok")
	resp, _ = http.DefaultClient.Do(del)
	resp.Body.Close()
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("文件应被删除，err=%v", err)
	}

	// 重复删除应幂等返回 200(不存在视为成功)。
	resp, _ = http.DefaultClient.Do(del)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestHandleAppInputFile_RejectsPathTraversal 验证 ?path=../escape 越界写入被拒,
// 沙箱外不会留下任何文件残留。
func TestHandleAppInputFile_RejectsPathTraversal(t *testing.T) {
	dataRoot := t.TempDir()
	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()

	// 显式 .. 段:应被 resolveScopePath 拒绝,返回 400。
	put, _ := http.NewRequest(http.MethodPut,
		srv.URL+"/v1/scopes/apps/app-1/input/file?path=../../etc/passwd",
		strings.NewReader("evil"))
	put.Header.Set("Authorization", "Bearer tok")
	resp, _ := http.DefaultClient.Do(put)
	resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// DELETE 也要拒绝同样的越界路径。
	del, _ := http.NewRequest(http.MethodDelete,
		srv.URL+"/v1/scopes/apps/app-1/input/file?path=../../etc/passwd", nil)
	del.Header.Set("Authorization", "Bearer tok")
	resp2, _ := http.DefaultClient.Do(del)
	resp2.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp2.StatusCode)
}

// TestScopesWorkspaceList 验证scope 工作区列表的预期行为场景。
func TestScopesWorkspaceList(t *testing.T) {
	dataRoot := t.TempDir()
	root := filepath.Join(dataRoot, "apps", "app-1", "data", "workspace")
	_ = os.MkdirAll(filepath.Join(root, "sub"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "report.pdf"), []byte("pdf"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "sub", "image.png"), []byte("png-bytes"), 0o644)

	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()

	// 列根目录
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/scopes/apps/app-1/workspace", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got struct {
		Path    string `json:"path"`
		Entries []struct {
			Name string `json:"name"`
			Type string `json:"type"`
			Size int64  `json:"size"`
		} `json:"entries"`
	}
	dec := jsonDecoder(resp.Body)
	err = dec.Decode(&got)
	require.NoError(t, err)
	require.Equal(t, 2, len(got.Entries))
	// 列子目录
	req2, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/scopes/apps/app-1/workspace?path=sub", nil)
	req2.Header.Set("Authorization", "Bearer tok")
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusOK, resp2.StatusCode)
}

// TestScopesWorkspaceList_NotExistReturnsEmpty 验证scope 工作区列表未Exist返回空值的成功路径场景。
func TestScopesWorkspaceList_NotExistReturnsEmpty(t *testing.T) {
	dataRoot := t.TempDir()
	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/scopes/apps/no-such-app/workspace", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestScopesWorkspaceDownload 验证scope 工作区下载的预期行为场景。
func TestScopesWorkspaceDownload(t *testing.T) {
	dataRoot := t.TempDir()
	root := filepath.Join(dataRoot, "apps", "app-1", "data", "workspace")
	_ = os.MkdirAll(root, 0o755)
	_ = os.WriteFile(filepath.Join(root, "out.txt"), []byte("hello"), 0o644)

	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/v1/scopes/apps/app-1/workspace/download?path=out.txt", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := readAll(resp.Body)
	require.Equal(t, "hello", string(body))
	require.True(t, strings.Contains(resp.Header.Get("Content-Disposition"), "out.txt"))
}

// TestScopesWorkspaceDownload_RejectsDirectory 验证scope 工作区下载拒绝目录的异常或拒绝路径场景。
func TestScopesWorkspaceDownload_RejectsDirectory(t *testing.T) {
	dataRoot := t.TempDir()
	root := filepath.Join(dataRoot, "apps", "app-1", "data", "workspace", "sub")
	_ = os.MkdirAll(root, 0o755)
	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/v1/scopes/apps/app-1/workspace/download?path=sub", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestScopesWorkspaceArchive 验证scope 工作区归档的预期行为场景。
func TestScopesWorkspaceArchive(t *testing.T) {
	dataRoot := t.TempDir()
	root := filepath.Join(dataRoot, "apps", "app-1", "data", "workspace")
	_ = os.MkdirAll(filepath.Join(root, "sub"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "a.txt"), []byte("AAA"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "sub", "b.txt"), []byte("BBB"), 0o644)

	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/v1/scopes/apps/app-1/workspace/archive", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := readAll(resp.Body)
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	require.NoError(t, err)
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["a.txt"] || !names["sub/b.txt"] {
		t.Fatalf("zip names=%v", names)
	}
}

// TestScopesAppArchive_MovesAndCleansUp 验证scope应用归档移动并清理的预期行为场景。
func TestScopesAppArchive_MovesAndCleansUp(t *testing.T) {
	dataRoot := t.TempDir()
	root := filepath.Join(dataRoot, "apps", "app-1")
	_ = os.MkdirAll(filepath.Join(root, "workspace"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "workspace", "out.pdf"), []byte("x"), 0o644)

	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()

	// 固定时间，便于断言归档目录名
	origNow := nowFunc
	t.Cleanup(func() { nowFunc = origNow })
	nowFunc = func() time.Time {
		return time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	}

	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/v1/scopes/apps/app-1/archive", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// 旧路径不在
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("旧路径应消失，err=%v", err)
	}
	// 归档路径在
	dest := filepath.Join(dataRoot, "archived", "app-1-20260502T120000Z")
	_, err = os.Stat(filepath.Join(dest, "workspace", "out.pdf"))
	require.NoError(t, err)

	// 二次 archive（应用目录已不在）应幂等成功
	resp2, _ := http.DefaultClient.Do(req)
	resp2.Body.Close()
	require.Equal(t, http.StatusOK, resp2.StatusCode)

	// 把归档 mtime 推回到 31 天前，调 cleanup
	old := nowFunc().Add(-31 * 24 * time.Hour)
	_ = os.Chtimes(dest, old, old)
	cleanReq, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/v1/scopes/cleanup-archives?retention_days=30", nil)
	cleanReq.Header.Set("Authorization", "Bearer tok")
	cleanResp, err := http.DefaultClient.Do(cleanReq)
	require.NoError(t, err)
	cleanResp.Body.Close()
	require.Equal(t, http.StatusOK, cleanResp.StatusCode)
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("归档应被清理，err=%v", err)
	}
}

// TestScopesCleanupArchives_KeepsRecent 验证scope清理归档保留近期的预期行为场景。
func TestScopesCleanupArchives_KeepsRecent(t *testing.T) {
	dataRoot := t.TempDir()
	keep := filepath.Join(dataRoot, "archived", "fresh-20260502T000000Z")
	_ = os.MkdirAll(keep, 0o755)
	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/v1/scopes/cleanup-archives?retention_days=7", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	_, err = os.Stat(keep)
	require.NoError(t, err)
}

// TestScopesCleanupArchives_RejectsBadRetention 验证scope清理归档拒绝非法保留时间的异常或拒绝路径场景。
func TestScopesCleanupArchives_RejectsBadRetention(t *testing.T) {
	dataRoot := t.TempDir()
	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()

	for _, v := range []string{"0", "-1", "abc", "999999999"} {
		req, _ := http.NewRequest(http.MethodPost,
			srv.URL+"/v1/scopes/cleanup-archives?retention_days="+v, nil)
		req.Header.Set("Authorization", "Bearer tok")
		resp, _ := http.DefaultClient.Do(req)
		resp.Body.Close()
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	}
}

// TestScopesAppInit_RejectsInvalidAppID 验证scope 应用初始化拒绝非法应用ID的异常或拒绝路径场景。
func TestScopesAppInit_RejectsInvalidAppID(t *testing.T) {
	dataRoot := t.TempDir()
	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()

	for _, bad := range []string{"../sneaky", ".secret", "with/slash", "with space"} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/scopes/apps/"+bad+"/init", nil)
		req.Header.Set("Authorization", "Bearer tok")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		// 路径里含 / 时 mux 解析路径会变；只要不是 200 即可
		require.NotEqual(t, http.StatusOK, resp.StatusCode)
	}
}

// TestScopesHandler_RequiresAuth 验证scope处理器要求认证的预期行为场景。
func TestScopesHandler_RequiresAuth(t *testing.T) {
	srv := httptest.NewServer(newHandlerWithDocker(t.TempDir(), nil, "tok"))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/scopes/apps/abc/init", nil)
	// 故意不带 Authorization
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
