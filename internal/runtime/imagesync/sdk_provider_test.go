package imagesync

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/registry"
	dockerclient "github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFakeDockerServer 返回一个 httptest server 用于模拟 docker daemon HTTP API。
// 路由覆盖本测试需要的三个端点：images/inspect、images/get(save)、images/create(pull)。
func newFakeDockerServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	return httptest.NewServer(h)
}

// newSDKClient 构造一个指向 fake daemon 的 docker client。
// 显式禁用 API version 协商（fake daemon 不实现 /_ping），避免握手阶段失败。
func newSDKClient(t *testing.T, baseURL string) *dockerclient.Client {
	t.Helper()
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.WithHost(baseURL),
		dockerclient.WithVersion("1.45"), // v27 daemon 默认 API 版本
	)
	require.NoError(t, err)
	return cli
}

// TestLocalDockerSDKProvider_ImageID 验证 inspect 解析 ID。
// docker SDK 的 ImageInspect 路径是 /<api-version>/images/<ref>/json,
// fake daemon 用 PathPrefix 匹配避开版本号差异。
func TestLocalDockerSDKProvider_ImageID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// 期望 inspect 路径以 /images/hermes-runtime:dev/json 结尾
		if !strings.HasSuffix(r.URL.Path, "/images/hermes-runtime:dev/json") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		// docker daemon ImageInspect 返回的 JSON 字段;sha256 前缀是常见格式。
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": "sha256:abc"})
	})
	srv := newFakeDockerServer(t, mux)
	defer srv.Close()

	cli := newSDKClient(t, srv.URL)
	prov := LocalDockerSDKProvider{cli: cli}

	id, err := prov.ImageID(context.Background(), "hermes-runtime:dev")
	require.NoError(t, err)
	assert.Equal(t, "sha256:abc", id)
}

// TestLocalDockerSDKProvider_Archive 验证 ImageSave 流式返回 tar bytes。
func TestLocalDockerSDKProvider_Archive(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/images/get") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		assert.Equal(t, "hermes-runtime:dev", r.URL.Query().Get("names"))
		w.Header().Set("Content-Type", "application/x-tar")
		_, _ = io.WriteString(w, "FAKE-TAR-PAYLOAD")
	})
	srv := newFakeDockerServer(t, mux)
	defer srv.Close()

	cli := newSDKClient(t, srv.URL)
	prov := LocalDockerSDKProvider{cli: cli}

	rc, err := prov.Archive(context.Background(), "hermes-runtime:dev")
	require.NoError(t, err)
	defer rc.Close()
	body, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, "FAKE-TAR-PAYLOAD", string(body))
}

// TestLocalDockerSDKProvider_Pull 验证 NDJSON 流被原样转发给调用方;
// 解析 / 累加由 imagecoord/aggregator 负责,本 provider 只透传字节流与 RegistryAuth 头。
func TestLocalDockerSDKProvider_Pull(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/images/create") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		assert.Equal(t, "hermes-runtime", r.URL.Query().Get("fromImage"))
		assert.Equal(t, "dev", r.URL.Query().Get("tag"))
		// X-Registry-Auth 必须是 base64.URLEncoding(JSON(authConfig))
		raw, err := base64.URLEncoding.DecodeString(r.Header.Get("X-Registry-Auth"))
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"username":"alice"`)

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"Pulling fs layer","id":"abc"}`+"\n")
		_, _ = io.WriteString(w, `{"status":"Pull complete","id":"abc"}`+"\n")
	})
	srv := newFakeDockerServer(t, mux)
	defer srv.Close()

	cli := newSDKClient(t, srv.URL)
	// 手工塞一条 auth,等价于 LoadRegistryAuthStore 解析后的状态
	store := RegistryAuthStore{auths: map[string]registry.AuthConfig{
		"docker.io": {Username: "alice", Password: "s3cret", ServerAddress: "docker.io"},
	}}
	prov := LocalDockerSDKProvider{cli: cli, authStore: store}

	rc, err := prov.Pull(context.Background(), "hermes-runtime:dev")
	require.NoError(t, err)
	defer rc.Close()
	body, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(body), "Pulling fs layer"))
	assert.True(t, strings.Contains(string(body), "Pull complete"))
}
