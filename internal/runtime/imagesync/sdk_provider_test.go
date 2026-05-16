package imagesync

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
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

// TestLocalDockerSDKProvider_ImageID 验证 ImageID 从 docker save 归档首个 tar 条目提取 sha256。
// docker save 归档的首个条目固定命名为 <sha256hex>.json（image config 文件），
// ImageID 从文件名提取 hex 并加 "sha256:" 前缀返回，与 docker load 落地 ID 一致。
func TestLocalDockerSDKProvider_ImageID(t *testing.T) {
	// 构造一个最小 tar 归档，首条目以 64 位 sha256hex.json 命名，模拟 docker save 输出。
	const wantHex = "9cf46248b69906ff754a1cd231720d707e4ea36f9b03e81d48f008f025c66f93"
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	configContent := []byte(`{"architecture":"amd64"}`)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name: wantHex + ".json",
		Size: int64(len(configContent)),
		Mode: 0644,
	}))
	_, err := tw.Write(configContent)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	tarBytes := tarBuf.Bytes()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// ImageID 现在通过 docker save（/images/get）获取 ID，不再走 inspect。
		if !strings.HasSuffix(r.URL.Path, "/images/get") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		assert.Equal(t, "hermes-runtime:dev", r.URL.Query().Get("names"))
		w.Header().Set("Content-Type", "application/x-tar")
		_, _ = w.Write(tarBytes)
	})
	srv := newFakeDockerServer(t, mux)
	defer srv.Close()

	cli := newSDKClient(t, srv.URL)
	prov := LocalDockerSDKProvider{cli: cli}

	id, err := prov.ImageID(context.Background(), "hermes-runtime:dev")
	require.NoError(t, err)
	// 期望从 tar 首条目文件名提取出 sha256: 前缀的完整 ID。
	assert.Equal(t, "sha256:"+wantHex, id)
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
