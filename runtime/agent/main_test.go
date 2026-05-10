package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"oc-manager/runtime/agent/config"
)

func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	newHandlerWithDocker("/tmp/agent", &fakeDockerClient{}, "").ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body HealthResponse
	err := json.NewDecoder(rec.Body).Decode(&body)
	require.NoError(t, err)
	if body.Status != "ok" || body.Role != "runtime-agent" || body.DataRoot != "/tmp/agent" {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestInspectImage(t *testing.T) {
	docker := &fakeDockerClient{
		images: map[string]DockerImageInfo{
			"openclaw-runtime:dev": {ID: "sha256:local", RepoTags: []string{"openclaw-runtime:dev"}},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/images/inspect?image=openclaw-runtime:dev", nil)
	rec := httptest.NewRecorder()

	newHandlerWithDocker("/tmp/agent", docker, "").ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Exists bool            `json:"exists"`
		Info   DockerImageInfo `json:"info"`
	}
	err := json.NewDecoder(rec.Body).Decode(&body)
	require.NoError(t, err)
	if !body.Exists || body.Info.ID != "sha256:local" {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestInspectImageNotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/images/inspect?image=missing:dev", nil)
	rec := httptest.NewRecorder()

	newHandlerWithDocker("/tmp/agent", &fakeDockerClient{}, "").ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Exists bool `json:"exists"`
	}
	err := json.NewDecoder(rec.Body).Decode(&body)
	require.NoError(t, err)
	require.False(t, body.Exists)
}

func TestLoadImageRequiresTokenWhenConfigured(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/images/load?image=openclaw-runtime:dev", bytes.NewBufferString("tar"))
	rec := httptest.NewRecorder()

	newHandlerWithDocker("/tmp/agent", &fakeDockerClient{}, "secret").ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestNewHandlerUsesConfiguredToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/images/inspect?image=openclaw-runtime:dev", nil)
	rec := httptest.NewRecorder()

	newHandler("/tmp/agent", "secret", "/var/run/docker.sock").ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestNewHandlerUsesConfiguredDockerSocketForImages(t *testing.T) {
	var inspected bool
	var loadedBytes string
	socket := startUnixDockerStub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/images/openclaw-runtime:dev/json":
			inspected = true
			writeJSON(w, map[string]any{
				"Id":       "sha256:configured",
				"RepoTags": []string{"openclaw-runtime:dev"},
			})
		case "/images/load":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read load body: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			loadedBytes = string(body)
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected docker path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	handler := newHandler("/tmp/agent", "secret", socket)

	inspectReq := httptest.NewRequest(http.MethodGet, "/v1/images/inspect?image=openclaw-runtime:dev", nil)
	inspectReq.Header.Set("Authorization", "Bearer secret")
	inspectRec := httptest.NewRecorder()
	handler.ServeHTTP(inspectRec, inspectReq)
	require.Equal(t, http.StatusOK, inspectRec.Code)
	require.True(t, inspected)

	loadReq := httptest.NewRequest(http.MethodPost, "/v1/images/load?image=openclaw-runtime:dev", bytes.NewBufferString("tar"))
	loadReq.Header.Set("Authorization", "Bearer secret")
	loadRec := httptest.NewRecorder()
	handler.ServeHTTP(loadRec, loadReq)
	require.Equal(t, http.StatusOK, loadRec.Code)
	require.Equal(t, "tar", loadedBytes)
}

func TestLoadImage(t *testing.T) {
	docker := &fakeDockerClient{images: map[string]DockerImageInfo{}}
	req := httptest.NewRequest(http.MethodPost, "/v1/images/load?image=openclaw-runtime:dev", bytes.NewBufferString("tar"))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()

	newHandlerWithDocker("/tmp/agent", docker, "secret").ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "tar", docker.loadedBytes)
}

// freePort 借助内核分配一个空闲 TCP 端口，避免测试在并发跑时端口冲突。
func freePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	return listener.Addr().String()
}

func TestRunAgent_PrintsCAPEMAndAcceptsTLS(t *testing.T) {
	stateDir := t.TempDir()
	dataRoot := t.TempDir()
	dockerSocket := startUnixDockerStub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_ping" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("OK"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	dockerAddr := freePort(t)
	fileAddr := freePort(t)

	opts := agentOptions{
		dataRoot:      dataRoot,
		stateDir:      stateDir,
		dockerSocket:  dockerSocket,
		hostname:      "127.0.0.1",
		dockerAddr:    dockerAddr,
		fileAddr:      fileAddr,
		dockerProxy:   true,
		enableSignals: false,
		fullConfig: config.Config{
			Manager: config.ManagerConfig{
				Endpoint:         "",
				EnrollmentSecret: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			},
		},
	}
	require.NoError(t, storeCredentials(stateDir, "node-id", "secret"))

	stdout := &bytes.Buffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runAgent(ctx, opts, stdout) }()

	caBundle := waitForCAPEM(t, stdout, 2*time.Second)
	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(caBundle))

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: caPool, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12},
		},
		Timeout: 2 * time.Second,
	}
	waitForTLSReady(t, httpClient, "https://"+dockerAddr+"/v1/docker/_ping", "secret", 2*time.Second)

	req, _ := http.NewRequest(http.MethodGet, "https://"+dockerAddr+"/v1/docker/_ping", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// 文件 API 与 docker proxy 都走 TLS，对照验证两个端口同时正常。
	plainResp, err := httpClient.Get("https://" + fileAddr + "/healthz")
	require.NoError(t, err)
	defer plainResp.Body.Close()
	require.Equal(t, http.StatusOK, plainResp.StatusCode)

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("runAgent 未在超时内退出")
	}
}

// waitForCAPEM 在超时内反复扫描 stdout，直到看到 agent-ca-pem-base64 行。
func waitForCAPEM(t *testing.T, stdout *bytes.Buffer, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if line := extractCAPEMLine(stdout.String()); line != "" {
			caBytes, err := base64.StdEncoding.DecodeString(line)
			require.NoError(t, err)
			return caBytes
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("超时：stdout 未输出 agent-ca-pem-base64; 当前 stdout=%q", stdout.String())
	return nil
}

func extractCAPEMLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		const prefix = "agent-ca-pem-base64: "
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

// waitForTLSReady 在超时内重试调用 endpoint 直到 TLS 监听就绪。
func waitForTLSReady(t *testing.T, client *http.Client, url, token string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("TLS 监听未就绪: %s", url)
}

type fakeDockerClient struct {
	images      map[string]DockerImageInfo
	loadedBytes string
}

func (f *fakeDockerClient) InspectImage(_ context.Context, image string) (DockerImageInfo, error) {
	if f.images == nil {
		return DockerImageInfo{}, ErrImageNotFound
	}
	info, ok := f.images[image]
	if !ok {
		return DockerImageInfo{}, ErrImageNotFound
	}
	return info, nil
}

func (f *fakeDockerClient) LoadImage(_ context.Context, archive io.Reader) error {
	body, err := io.ReadAll(archive)
	if err != nil {
		return err
	}
	f.loadedBytes = string(body)
	if f.images == nil {
		f.images = map[string]DockerImageInfo{}
	}
	f.images["openclaw-runtime:dev"] = DockerImageInfo{ID: "sha256:loaded", RepoTags: []string{"openclaw-runtime:dev"}}
	return nil
}
