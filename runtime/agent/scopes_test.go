package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
