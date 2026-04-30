package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// startUnixDockerStub 在临时 unix socket 上挂一个 mock docker daemon，并返回 socket 路径。
func startUnixDockerStub(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "docker.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	t.Cleanup(func() {
		_ = server.Close()
		_ = os.Remove(socketPath)
	})
	return socketPath
}

func TestDockerProxy_ForwardsRewrittenPath(t *testing.T) {
	var seenPath string
	socket := startUnixDockerStub(t, func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"OK":true}`))
	})

	handler := NewDockerProxyHandler(socket, "secret", "")
	req := httptest.NewRequest(http.MethodGet, "/v1/docker/_ping", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if seenPath != "/_ping" {
		t.Fatalf("docker daemon 收到的 path = %q, want /_ping（前缀必须重写）", seenPath)
	}
}

func TestDockerProxy_RejectsMissingToken(t *testing.T) {
	socket := startUnixDockerStub(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("不应转发到 docker: %s", r.URL.Path)
	})
	handler := NewDockerProxyHandler(socket, "secret", "")

	req := httptest.NewRequest(http.MethodGet, "/v1/docker/_ping", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestDockerProxy_RejectsWrongToken(t *testing.T) {
	socket := startUnixDockerStub(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("不应转发到 docker")
	})
	handler := NewDockerProxyHandler(socket, "secret", "")

	req := httptest.NewRequest(http.MethodGet, "/v1/docker/_ping", nil)
	req.Header.Set("Authorization", "Bearer other")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestDockerProxy_RejectsOutsideCIDR(t *testing.T) {
	socket := startUnixDockerStub(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("不应转发到 docker")
	})
	handler := NewDockerProxyHandler(socket, "secret", "10.0.0.0/24")

	req := httptest.NewRequest(http.MethodGet, "/v1/docker/_ping", nil)
	req.Header.Set("Authorization", "Bearer secret")
	req.RemoteAddr = "192.168.1.5:34567"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestDockerProxy_AllowsInsideCIDR(t *testing.T) {
	socket := startUnixDockerStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := NewDockerProxyHandler(socket, "secret", "10.0.0.0/24")
	req := httptest.NewRequest(http.MethodGet, "/v1/docker/_ping", nil)
	req.Header.Set("Authorization", "Bearer secret")
	req.RemoteAddr = "10.0.0.5:34567"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestDockerProxy_PreservesBodyAndStatus(t *testing.T) {
	socket := startUnixDockerStub(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != "payload" {
			t.Errorf("docker 收到 body = %q, want payload", body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"created":true}`))
	})
	handler := NewDockerProxyHandler(socket, "secret", "")
	req := httptest.NewRequest(http.MethodPost, "/v1/docker/containers/create", strings.NewReader("payload"))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "created") {
		t.Fatalf("body = %q, want 含 created", rec.Body.String())
	}
}

func TestDockerProxy_NonDockerPathReturns404(t *testing.T) {
	socket := startUnixDockerStub(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("不应转发到 docker: %s", r.URL.Path)
	})
	handler := NewDockerProxyHandler(socket, "", "")
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestDockerProxy_NoTokenSkipsAuth(t *testing.T) {
	// agentToken="" 时仅用于本地调试场景：不强制 bearer，便于 curl --unix-socket 验证。
	socket := startUnixDockerStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := NewDockerProxyHandler(socket, "", "")
	req := httptest.NewRequest(http.MethodGet, "/v1/docker/_ping", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestDockerProxy_WithRealUnixDial(t *testing.T) {
	// 端到端：使用真实 unix dial，校验 Director + Transport 完整链路。
	socket := startUnixDockerStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":"stub"}`))
	})
	handler := NewDockerProxyHandler(socket, "", "")
	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/v1/docker/version")
	if err != nil {
		t.Fatalf("Get err = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "stub") {
		t.Fatalf("body = %s", body)
	}
	_ = time.Millisecond // 占位避免 import 被精简
}
