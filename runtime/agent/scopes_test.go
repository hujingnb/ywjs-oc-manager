package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
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

func TestScopesHandler_NotImplementedReturns501(t *testing.T) {
	srv := httptest.NewServer(newHandlerWithDocker(t.TempDir(), nil, "tok"))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/scopes/apps/abc/init", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status=%d, want 501", resp.StatusCode)
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
