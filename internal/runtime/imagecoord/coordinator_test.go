package imagecoord

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dockerclient "github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ocredis "oc-manager/internal/redis"
)

// newTestDockerClient 构造一个指向 fake daemon 的 docker client。
// 显式禁用 API version 协商（fake daemon 不实现 /_ping），避免握手阶段失败。
func newTestDockerClient(t *testing.T, baseURL string) *dockerclient.Client {
	t.Helper()
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.WithHost(baseURL),
		dockerclient.WithVersion("1.45"),
	)
	require.NoError(t, err)
	return cli
}

// fakeLocker 控制 TryAcquire 是否成功；其余方法直接返回零值，适合单元测试。
type fakeLocker struct {
	acquireOK bool
}

func (l *fakeLocker) TryAcquire(_ context.Context, _, _ string, _ time.Duration) (bool, error) {
	return l.acquireOK, nil
}
func (l *fakeLocker) Renew(_ context.Context, _, _ string, _ time.Duration) error { return nil }
func (l *fakeLocker) Release(_ context.Context, _, _ string) error                { return nil }
func (l *fakeLocker) Exists(_ context.Context, _ string) (bool, error)            { return false, nil }

// fakeBus 丢弃所有发布事件，Subscribe 返回立即关闭的 channel，供单元测试使用。
type fakeBus struct{}

func (b *fakeBus) Publish(_ context.Context, _ string, _ ProgressEvent) error { return nil }
func (b *fakeBus) PublishDone(_ context.Context, _ string, _ error) error      { return nil }
func (b *fakeBus) Subscribe(_ context.Context, _ ...string) (<-chan ocredis.BusMessage, func(), error) {
	ch := make(chan ocredis.BusMessage)
	return ch, func() { close(ch) }, nil
}

// newFakeDockerHandler 构造一个极简 http.Handler 模拟 docker daemon HTTP API。
// imagePresent=true 时 /images/<name>/json 返回 200 带 fakeID；否则返回 404。
// pullStream 是 /images/create 端点返回的 NDJSON 内容（pull 进度流）。
func newFakeDockerHandler(imagePresent bool, pullStream string) (http.Handler, string) {
	const fakeID = "sha256:9cf46248b69906ff754a1cd231720d707e4ea36f9b03e81d48f008f025c66f93"
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.Contains(path, "/images/") && strings.HasSuffix(path, "/json"):
			// ImageInspectWithRaw
			if !imagePresent {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"No such image"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"` + fakeID + `","RepoTags":["hermes:v1"]}`))
		case strings.Contains(path, "/images/create"):
			// ImagePull
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(pullStream))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	return mux, fakeID
}

// TestCoordinator_PullImageOnNode_AlreadyPresent 镜像已在节点上时直接返回 sha256，不触发 pull。
func TestCoordinator_PullImageOnNode_AlreadyPresent(t *testing.T) {
	// 场景：phasePullRuntimeImage 重入路径，镜像已存在，应零开销直接返回。
	handler, wantID := newFakeDockerHandler(true, "")
	srv := httptest.NewServer(handler)
	defer srv.Close()
	cli := newTestDockerClient(t, srv.URL)

	coord := NewCoordinator(&fakeLocker{acquireOK: true}, &fakeBus{}, "test-instance")
	sub := make(chan ProgressEvent, 4)

	id, err := coord.PullImageOnNode(context.Background(), "node-1", "hermes:v1", cli, sub)
	require.NoError(t, err)
	assert.Equal(t, wantID, id)
	// 镜像已存在时不应有任何进度事件
	assert.Empty(t, sub)
}

// TestCoordinator_PullImageOnNode_Leader 镜像不存在时作为 leader 执行 pull 并返回 sha256。
func TestCoordinator_PullImageOnNode_Leader(t *testing.T) {
	// 场景：首次部署，节点上不存在 hermes 镜像，leader 执行 pull 后返回 sha256。
	callCount := 0
	const fakeID = "sha256:9cf46248b69906ff754a1cd231720d707e4ea36f9b03e81d48f008f025c66f93"
	pullNDJSON := `{"status":"Pulling fs layer","id":"abc","progressDetail":{"current":100,"total":200}}` + "\n" +
		`{"status":"Pull complete","id":"abc","progressDetail":{}}` + "\n"

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.Contains(path, "/images/") && strings.HasSuffix(path, "/json") {
			callCount++
			if callCount == 1 {
				// 首次 inspect：镜像不存在
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"No such image"}`))
				return
			}
			// 二次 inspect（pull 完成后）：镜像存在
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"` + fakeID + `","RepoTags":["hermes:v1"]}`))
			return
		}
		if strings.Contains(path, "/images/create") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(pullNDJSON))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cli := newTestDockerClient(t, srv.URL)
	coord := NewCoordinator(&fakeLocker{acquireOK: true}, &fakeBus{}, "test-instance")
	sub := make(chan ProgressEvent, 16)

	id, err := coord.PullImageOnNode(context.Background(), "node-1", "hermes:v1", cli, sub)
	require.NoError(t, err)
	assert.Equal(t, fakeID, id)
	// 应有至少一个进度事件（ticker 或 done 发送的）
	assert.NotEmpty(t, sub)
}

// TestCoordinator_PullImageOnNode_Follower follower 路径：lock 已不存在时直接 inspect 获取 sha256。
func TestCoordinator_PullImageOnNode_Follower(t *testing.T) {
	// 场景：同一节点同一镜像并发部署，follower 等 leader 完成后自行 inspect。
	const fakeID = "sha256:9cf46248b69906ff754a1cd231720d707e4ea36f9b03e81d48f008f025c66f93"

	// 模拟首次 inspect 返回 404（触发锁竞争），后续 inspect 返回 200（leader 已完成）。
	callCount := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.Contains(path, "/images/") && strings.HasSuffix(path, "/json") {
			callCount++
			if callCount == 1 {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"No such image"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"` + fakeID + `","RepoTags":["hermes:v1"]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cli := newTestDockerClient(t, srv.URL)
	// follower 抢锁失败（acquireOK=false），Exists 返回 false（leader 已完成）
	coord := NewCoordinator(&fakeLocker{acquireOK: false}, &fakeBus{}, "test-instance")
	sub := make(chan ProgressEvent, 4)

	id, err := coord.PullImageOnNode(context.Background(), "node-1", "hermes:v1", cli, sub)
	require.NoError(t, err)
	assert.Equal(t, fakeID, id)
}
