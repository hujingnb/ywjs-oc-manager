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
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/assert"
)

// startUnixDockerStub 在临时 unix socket 上挂一个 mock docker daemon，并返回 socket 路径。
func startUnixDockerStub(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "docker.sock")
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	t.Cleanup(func() {
		_ = server.Close()
		_ = os.Remove(socketPath)
	})
	return socketPath
}

// TestDockerProxy_ForwardsRewrittenPath 验证Docker 代理转发重写后路径的预期行为场景。
func TestDockerProxy_ForwardsRewrittenPath(t *testing.T) {
	var seenPath string
	socket := startUnixDockerStub(t, func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"OK":true}`))
	})

	handler := NewDockerProxyHandler(socket, "secret", "", "", "")
	req := httptest.NewRequest(http.MethodGet, "/v1/docker/_ping", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "/_ping", seenPath)
}

// TestDockerProxy_RejectsMissingToken 验证Docker 代理拒绝缺失令牌的异常或拒绝路径场景。
func TestDockerProxy_RejectsMissingToken(t *testing.T) {
	socket := startUnixDockerStub(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("不应转发到 docker: %s", r.URL.Path)
	})
	handler := NewDockerProxyHandler(socket, "secret", "", "", "")

	req := httptest.NewRequest(http.MethodGet, "/v1/docker/_ping", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestDockerProxy_RejectsWrongToken 验证Docker 代理拒绝错误令牌的异常或拒绝路径场景。
func TestDockerProxy_RejectsWrongToken(t *testing.T) {
	socket := startUnixDockerStub(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("不应转发到 docker")
	})
	handler := NewDockerProxyHandler(socket, "secret", "", "", "")

	req := httptest.NewRequest(http.MethodGet, "/v1/docker/_ping", nil)
	req.Header.Set("Authorization", "Bearer other")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestDockerProxy_RejectsOutsideCIDR 验证Docker 代理拒绝CIDR 网段外地址的异常或拒绝路径场景。
func TestDockerProxy_RejectsOutsideCIDR(t *testing.T) {
	socket := startUnixDockerStub(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("不应转发到 docker")
	})
	handler := NewDockerProxyHandler(socket, "secret", "10.0.0.0/24", "", "")

	req := httptest.NewRequest(http.MethodGet, "/v1/docker/_ping", nil)
	req.Header.Set("Authorization", "Bearer secret")
	req.RemoteAddr = "192.168.1.5:34567"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// TestDockerProxy_AllowsInsideCIDR 验证Docker 代理允许CIDR 网段内地址的预期行为场景。
func TestDockerProxy_AllowsInsideCIDR(t *testing.T) {
	socket := startUnixDockerStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := NewDockerProxyHandler(socket, "secret", "10.0.0.0/24", "", "")
	req := httptest.NewRequest(http.MethodGet, "/v1/docker/_ping", nil)
	req.Header.Set("Authorization", "Bearer secret")
	req.RemoteAddr = "10.0.0.5:34567"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

// TestDockerProxy_PreservesBodyAndStatus 验证Docker 代理保留请求体并状态的预期行为场景。
func TestDockerProxy_PreservesBodyAndStatus(t *testing.T) {
	socket := startUnixDockerStub(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		assert.Equal(t, "payload", string(body))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"created":true}`))
	})
	handler := NewDockerProxyHandler(socket, "secret", "", "", "")
	req := httptest.NewRequest(http.MethodPost, "/v1/docker/containers/create", strings.NewReader("payload"))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.True(t, strings.Contains(rec.Body.String(), "created"))
}

// TestDockerProxy_NonDockerPathReturns404 验证Docker 代理非 Docker路径返回404的成功路径场景。
func TestDockerProxy_NonDockerPathReturns404(t *testing.T) {
	socket := startUnixDockerStub(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("不应转发到 docker: %s", r.URL.Path)
	})
	handler := NewDockerProxyHandler(socket, "", "", "", "")
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// TestDockerProxy_NoTokenSkipsAuth 验证Docker 代理无令牌跳过认证的特殊分支或幂等场景。
func TestDockerProxy_NoTokenSkipsAuth(t *testing.T) {
	// agentToken="" 时仅用于本地调试场景：不强制 bearer，便于 curl --unix-socket 验证。
	socket := startUnixDockerStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := NewDockerProxyHandler(socket, "", "", "", "")
	req := httptest.NewRequest(http.MethodGet, "/v1/docker/_ping", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

// TestDockerProxy_WithRealUnixDial 验证Docker 代理使用真实UnixDial的预期行为场景。
func TestDockerProxy_WithRealUnixDial(t *testing.T) {
	// 端到端：使用真实 unix dial，校验 Director + Transport 完整链路。
	socket := startUnixDockerStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":"stub"}`))
	})
	handler := NewDockerProxyHandler(socket, "", "", "", "")
	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/v1/docker/version")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	require.True(t, strings.Contains(string(body), "stub"))
	_ = time.Millisecond // 占位避免 import 被精简
}

// TestDockerProxy_RewriteCreateContainerMounts 验证Docker 代理重写创建容器挂载的预期行为场景。
func TestDockerProxy_RewriteCreateContainerMounts(t *testing.T) {
	var seenBody []byte
	socket := startUnixDockerStub(t, func(w http.ResponseWriter, r *http.Request) {
		seenBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"Id":"abc"}`))
	})

	handler := NewDockerProxyHandler(socket, "", "",
		"/var/lib/oc-agent",
		"/host/data/agent")

	body := `{
		"Image":"hermes-runtime:dev",
		"HostConfig":{
			"Binds":["/var/lib/oc-agent/apps/x/workspace:/workspace:rw","/etc/timezone:/etc/timezone:ro"],
			"Mounts":[
				{"Type":"bind","Source":"/var/lib/oc-agent/apps/x/openclaw-config/models.json","Target":"/root/models.json"},
				{"Type":"bind","Source":"/var/lib/oc-agent-2/skip","Target":"/skip"},
				{"Type":"bind","Source":"/var/lib/oc-agent","Target":"/exact"}
			]
		}
	}`
	req := httptest.NewRequest(http.MethodPost,
		"/v1/docker/v1.43/containers/create?name=ocm-x", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = int64(len(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	got := string(seenBody)
	// HostConfig.Binds 第一条 src 改写
	assert.Contains(t, got, `/host/data/agent/apps/x/workspace:/workspace:rw`)
	// 非 oc-agent 前缀的 bind 不变
	assert.Contains(t, got, `/etc/timezone:/etc/timezone:ro`)
	// HostConfig.Mounts[0].Source 改写
	assert.Contains(t, got, `"Source":"/host/data/agent/apps/x/openclaw-config/models.json"`)
	// /var/lib/oc-agent-2 不应被误改（前缀严格匹配）
	assert.Contains(t, got, `"Source":"/var/lib/oc-agent-2/skip"`)
	// /var/lib/oc-agent 整 mount 也要改写
	assert.Contains(t, got, `"Source":"/host/data/agent"`)
}

// TestDockerProxy_RewriteSkippedWhenSamePath 验证Docker 代理重写Skipped当相同路径的预期行为场景。
func TestDockerProxy_RewriteSkippedWhenSamePath(t *testing.T) {
	var seenBody []byte
	socket := startUnixDockerStub(t, func(w http.ResponseWriter, r *http.Request) {
		seenBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	})
	// agentDataRoot == hostDataRoot（agent 与 host 同路径，dev 场景）→ 不重写。
	handler := NewDockerProxyHandler(socket, "", "",
		"/var/lib/oc-agent",
		"/var/lib/oc-agent")

	body := `{"HostConfig":{"Binds":["/var/lib/oc-agent/x:/y:rw"]}}`
	req := httptest.NewRequest(http.MethodPost,
		"/v1/docker/v1.43/containers/create", strings.NewReader(body))
	req.ContentLength = int64(len(body))
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if !strings.Contains(string(seenBody), `/var/lib/oc-agent/x:/y:rw`) {
		t.Errorf("不应重写但 body 变了：%s", seenBody)
	}
}

// TestDockerProxy_RewriteOnlyForCreateContainer 验证Docker 代理重写仅针对创建容器的预期行为场景。
func TestDockerProxy_RewriteOnlyForCreateContainer(t *testing.T) {
	var seenBody []byte
	socket := startUnixDockerStub(t, func(w http.ResponseWriter, r *http.Request) {
		seenBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})
	handler := NewDockerProxyHandler(socket, "", "", "/var/lib/oc-agent", "/host")

	// /containers/abc/start 不应触发重写
	body := `{"HostConfig":{"Binds":["/var/lib/oc-agent/x:/y:rw"]}}`
	req := httptest.NewRequest(http.MethodPost,
		"/v1/docker/v1.43/containers/abc/start", strings.NewReader(body))
	req.ContentLength = int64(len(body))
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if !strings.Contains(string(seenBody), `/var/lib/oc-agent/x:/y:rw`) {
		t.Errorf("/containers/abc/start 不应重写但被改：%s", seenBody)
	}
}
