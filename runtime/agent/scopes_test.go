package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// jsonDecoder 与 readAll 是测试辅助 helper（避免重复 import）。
func jsonDecoder(r io.Reader) *json.Decoder { return json.NewDecoder(r) }
func readAll(r io.Reader) ([]byte, error)    { return io.ReadAll(r) }

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
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	return &buf
}

func TestResolveScopePath(t *testing.T) {
	dataRoot := t.TempDir()
	scope := "apps/abc"

	cases := []struct {
		name    string
		rel     string
		wantErr bool
	}{
		{"empty rel returns scope root", "", false},
		{"slash returns scope root", "/", false},
		{"clean nested file", "workspace/foo.txt", false},
		{"deep clean path", "knowledge/sub/dir/file.pdf", false},
		{"rel with dot dot rejected", "../bbb", true},
		{"abs path rejected", "/etc/passwd", true},
		{"hidden traversal rejected", "workspace/../../etc/passwd", true},
		{"trailing dot dot rejected", "workspace/..", true},
		{"sibling escape rejected", "../../apps/other/workspace", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			abs, err := resolveScopePath(dataRoot, scope, c.rel)
			if c.wantErr {
				if !errors.Is(err, ErrInvalidPath) {
					t.Fatalf("want ErrInvalidPath, got abs=%s err=%v", abs, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			scopeRoot, _ := filepath.Abs(filepath.Join(dataRoot, scope))
			if abs != scopeRoot && !strings.HasPrefix(abs+string(filepath.Separator), scopeRoot+string(filepath.Separator)) {
				t.Fatalf("abs=%s not under scopeRoot=%s", abs, scopeRoot)
			}
		})
	}
}

func TestScopesHandler_UnknownActionReturns404(t *testing.T) {
	srv := httptest.NewServer(newHandlerWithDocker(t.TempDir(), nil, "tok"))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/scopes/apps/abc/no-such-action", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", resp.StatusCode)
	}
}

func TestScopesAppInit_CreatesFourDirs(t *testing.T) {
	dataRoot := t.TempDir()
	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/scopes/apps/app-123/init", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	for _, sub := range []string{"knowledge", "workspace", "state", "logs"} {
		dir := filepath.Join(dataRoot, "apps", "app-123", sub)
		fi, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("dir %q not created: %v", sub, err)
		}
		if !fi.IsDir() {
			t.Fatalf("%q not a directory", sub)
		}
	}
}

func TestScopesAppInit_Idempotent(t *testing.T) {
	dataRoot := t.TempDir()
	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()

	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/scopes/apps/app-123/init", nil)
		req.Header.Set("Authorization", "Bearer tok")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("i=%d err=%v", i, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("i=%d status=%d", i, resp.StatusCode)
		}
	}
}

func TestScopesKnowledgeSync_App_ReplaceContents(t *testing.T) {
	dataRoot := t.TempDir()
	stale := filepath.Join(dataRoot, "apps", "app-1", "knowledge", "stale.txt")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

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
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}

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

func TestScopesKnowledgeSync_Org_CreatesPath(t *testing.T) {
	dataRoot := t.TempDir()
	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()

	body := makeTar(t, map[string]string{"intro.md": "# org"})
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/v1/scopes/orgs/org-1/knowledge/sync", body)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	got, err := os.ReadFile(filepath.Join(dataRoot, "orgs", "org-1", "knowledge", "intro.md"))
	if err != nil || string(got) != "# org" {
		t.Fatalf("intro.md = %q, %v", got, err)
	}
}

func TestScopesKnowledgeSync_RejectsTraversalEntry(t *testing.T) {
	dataRoot := t.TempDir()
	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()

	body := makeTar(t, map[string]string{"../../etc/passwd": "evil"})
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/v1/scopes/apps/app-1/knowledge/sync", body)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.StatusCode)
	}
	if _, err := os.Stat("/etc/passwd-evil"); err == nil {
		t.Fatalf("应当拒绝写出 scope")
	}
}

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
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	dir := filepath.Join(dataRoot, "apps", "app-1", "knowledge")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("knowledge dir 应存在: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("knowledge dir 应为空，含 %d 项", len(entries))
	}
}

func TestScopesKnowledgeFile_PutAndDelete(t *testing.T) {
	dataRoot := t.TempDir()
	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()

	// 上传一个文件
	put, _ := http.NewRequest(http.MethodPut,
		srv.URL+"/v1/scopes/apps/app-1/knowledge/file?path=sub/dir/note.txt",
		strings.NewReader("hello world"))
	put.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(put)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status=%d", resp.StatusCode)
	}
	dest := filepath.Join(dataRoot, "apps", "app-1", "knowledge", "sub", "dir", "note.txt")
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "hello world" {
		t.Fatalf("file content = %q, %v", got, err)
	}

	// 覆盖写入（同名）
	put2, _ := http.NewRequest(http.MethodPut,
		srv.URL+"/v1/scopes/apps/app-1/knowledge/file?path=sub/dir/note.txt",
		strings.NewReader("v2"))
	put2.Header.Set("Authorization", "Bearer tok")
	resp, _ = http.DefaultClient.Do(put2)
	resp.Body.Close()
	got, _ = os.ReadFile(dest)
	if string(got) != "v2" {
		t.Fatalf("覆盖后 = %q", got)
	}

	// 删除
	del, _ := http.NewRequest(http.MethodDelete,
		srv.URL+"/v1/scopes/apps/app-1/knowledge/file?path=sub/dir/note.txt", nil)
	del.Header.Set("Authorization", "Bearer tok")
	resp, _ = http.DefaultClient.Do(del)
	resp.Body.Close()
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("文件应被删除，err=%v", err)
	}

	// 删除不存在文件视为成功（幂等）
	resp, _ = http.DefaultClient.Do(del)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("二次删除应 200，得 %d", resp.StatusCode)
	}
}

func TestScopesKnowledgeFile_RejectsTraversalPath(t *testing.T) {
	dataRoot := t.TempDir()
	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()

	put, _ := http.NewRequest(http.MethodPut,
		srv.URL+"/v1/scopes/apps/app-1/knowledge/file?path=../../etc/passwd",
		strings.NewReader("evil"))
	put.Header.Set("Authorization", "Bearer tok")
	resp, _ := http.DefaultClient.Do(put)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("应返回 400，得 %d", resp.StatusCode)
	}
}

func TestScopesKnowledgeFile_OrgScope(t *testing.T) {
	dataRoot := t.TempDir()
	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()

	put, _ := http.NewRequest(http.MethodPut,
		srv.URL+"/v1/scopes/orgs/org-1/knowledge/file?path=intro.md",
		strings.NewReader("# org"))
	put.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(put)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	got, _ := os.ReadFile(filepath.Join(dataRoot, "orgs", "org-1", "knowledge", "intro.md"))
	if string(got) != "# org" {
		t.Fatalf("got=%q", got)
	}
}

func TestScopesWorkspaceList(t *testing.T) {
	dataRoot := t.TempDir()
	root := filepath.Join(dataRoot, "apps", "app-1", "workspace")
	_ = os.MkdirAll(filepath.Join(root, "sub"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "report.pdf"), []byte("pdf"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "sub", "image.png"), []byte("png-bytes"), 0o644)

	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()

	// 列根目录
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/scopes/apps/app-1/workspace", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var got struct {
		Path    string `json:"path"`
		Entries []struct {
			Name string `json:"name"`
			Type string `json:"type"`
			Size int64  `json:"size"`
		} `json:"entries"`
	}
	dec := jsonDecoder(resp.Body)
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("entries=%+v", got.Entries)
	}
	// 列子目录
	req2, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/scopes/apps/app-1/workspace?path=sub", nil)
	req2.Header.Set("Authorization", "Bearer tok")
	resp2, _ := http.DefaultClient.Do(req2)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("sub status=%d", resp2.StatusCode)
	}
}

func TestScopesWorkspaceList_NotExistReturnsEmpty(t *testing.T) {
	dataRoot := t.TempDir()
	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/scopes/apps/no-such-app/workspace", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestScopesWorkspaceDownload(t *testing.T) {
	dataRoot := t.TempDir()
	root := filepath.Join(dataRoot, "apps", "app-1", "workspace")
	_ = os.MkdirAll(root, 0o755)
	_ = os.WriteFile(filepath.Join(root, "out.txt"), []byte("hello"), 0o644)

	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/v1/scopes/apps/app-1/workspace/download?path=out.txt", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body, _ := readAll(resp.Body)
	if string(body) != "hello" {
		t.Fatalf("body=%q", body)
	}
	if !strings.Contains(resp.Header.Get("Content-Disposition"), "out.txt") {
		t.Fatalf("disposition=%q", resp.Header.Get("Content-Disposition"))
	}
}

func TestScopesWorkspaceDownload_RejectsDirectory(t *testing.T) {
	dataRoot := t.TempDir()
	root := filepath.Join(dataRoot, "apps", "app-1", "workspace", "sub")
	_ = os.MkdirAll(root, 0o755)
	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/v1/scopes/apps/app-1/workspace/download?path=sub", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestScopesWorkspaceArchive(t *testing.T) {
	dataRoot := t.TempDir()
	root := filepath.Join(dataRoot, "apps", "app-1", "workspace")
	_ = os.MkdirAll(filepath.Join(root, "sub"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "a.txt"), []byte("AAA"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "sub", "b.txt"), []byte("BBB"), 0o644)

	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/v1/scopes/apps/app-1/workspace/archive", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body, _ := readAll(resp.Body)
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("zip parse: %v", err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["a.txt"] || !names["sub/b.txt"] {
		t.Fatalf("zip names=%v", names)
	}
}

func TestScopesAppInit_RejectsInvalidAppID(t *testing.T) {
	dataRoot := t.TempDir()
	srv := httptest.NewServer(newHandlerWithDocker(dataRoot, nil, "tok"))
	defer srv.Close()

	for _, bad := range []string{"../sneaky", ".secret", "with/slash", "with space"} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/scopes/apps/"+bad+"/init", nil)
		req.Header.Set("Authorization", "Bearer tok")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		_ = resp.Body.Close()
		// 路径里含 / 时 mux 解析路径会变；只要不是 200 即可
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("bad app id %q got 200", bad)
		}
	}
}

func TestScopesHandler_RequiresAuth(t *testing.T) {
	srv := httptest.NewServer(newHandlerWithDocker(t.TempDir(), nil, "tok"))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/scopes/apps/abc/init", nil)
	// 故意不带 Authorization
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", resp.StatusCode)
	}
}
