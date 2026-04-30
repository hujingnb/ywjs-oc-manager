package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/client"

	"oc-manager/internal/integrations/agent"
	"oc-manager/internal/runtime/imagesync"
)

func TestAgentBackedAdapterEnsureImageDelegatesToImageSyncer(t *testing.T) {
	syncer := &fakeImageSyncer{result: imagesync.SyncResult{Image: "openclaw:dev", NodeID: "node-1", Transferred: true}}
	adapter := NewAgentBackedAdapter(nil, nil, syncer)

	got, err := adapter.EnsureImage(context.Background(), "node-1", "openclaw:dev")
	if err != nil {
		t.Fatalf("EnsureImage() error = %v", err)
	}
	if !got.Transferred {
		t.Fatalf("expected Transferred=true, got %+v", got)
	}
	if syncer.lastImage != "openclaw:dev" || syncer.lastNode != "node-1" {
		t.Fatalf("syncer last = %s/%s", syncer.lastNode, syncer.lastImage)
	}
}

func TestAgentBackedAdapterEnsureImageReturnsUnimplementedWithoutSyncer(t *testing.T) {
	adapter := NewAgentBackedAdapter(nil, nil, nil)
	if _, err := adapter.EnsureImage(context.Background(), "node-1", "openclaw:dev"); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("EnsureImage() error = %v, want ErrUnimplemented", err)
	}
}

func TestAgentBackedAdapterContainerOpsReturnUnimplemented(t *testing.T) {
	adapter := NewAgentBackedAdapter(nil, nil, nil)
	if _, err := adapter.CreateContainer(context.Background(), "n1", ContainerSpec{}); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("CreateContainer() error = %v", err)
	}
	if err := adapter.StartContainer(context.Background(), "n1", "c1"); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("StartContainer() error = %v", err)
	}
	if err := adapter.StopContainer(context.Background(), "n1", "c1"); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("StopContainer() error = %v", err)
	}
	if err := adapter.RestartContainer(context.Background(), "n1", "c1"); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("RestartContainer() error = %v", err)
	}
	if err := adapter.RemoveContainer(context.Background(), "n1", "c1"); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("RemoveContainer() error = %v", err)
	}
	if _, err := adapter.InspectContainer(context.Background(), "n1", "c1"); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("InspectContainer() error = %v", err)
	}
}

func TestAgentBackedAdapterFileOpsRouteThroughAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/files/list":
			_, _ = w.Write([]byte(`{"path":"/data","entries":[]}`))
		case "/v1/files/get":
			_, _ = w.Write([]byte("payload"))
		case "/v1/files/put":
			body, _ := io.ReadAll(r.Body)
			if string(body) != "hello" {
				t.Errorf("upload body = %q", string(body))
			}
			w.WriteHeader(http.StatusNoContent)
		case "/v1/files/delete":
			w.WriteHeader(http.StatusNoContent)
		case "/v1/files/archive":
			_, _ = w.Write([]byte("tar-bytes"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	resolver := fakeResolverFn(func(_ context.Context, nodeID string) (*agent.AgentFileClient, error) {
		if nodeID != "node-1" {
			t.Fatalf("nodeID = %q", nodeID)
		}
		return agent.NewFileClient(server.URL, ""), nil
	})
	adapter := NewAgentBackedAdapter(resolver, nil, nil)

	if _, err := adapter.ListFiles(context.Background(), "node-1", "/data"); err != nil {
		t.Fatalf("ListFiles() error = %v", err)
	}
	if err := adapter.UploadFile(context.Background(), "node-1", "/data/x.txt", strings.NewReader("hello")); err != nil {
		t.Fatalf("UploadFile() error = %v", err)
	}
	stream, err := adapter.DownloadFile(context.Background(), "node-1", "/data/x.txt")
	if err != nil {
		t.Fatalf("DownloadFile() error = %v", err)
	}
	if _, err := io.Copy(io.Discard, stream); err != nil {
		t.Fatalf("read download: %v", err)
	}
	stream.Close()
	if err := adapter.DeletePath(context.Background(), "node-1", "/data/x.txt"); err != nil {
		t.Fatalf("DeletePath() error = %v", err)
	}
	tar, err := adapter.ArchiveDirectory(context.Background(), "node-1", "/data")
	if err != nil {
		t.Fatalf("ArchiveDirectory() error = %v", err)
	}
	tarBody, _ := io.ReadAll(tar)
	tar.Close()
	if string(tarBody) != "tar-bytes" {
		t.Fatalf("archive body = %q", string(tarBody))
	}
}

func TestAgentBackedAdapterFileOpsRequireResolver(t *testing.T) {
	adapter := NewAgentBackedAdapter(nil, nil, nil)
	if _, err := adapter.ListFiles(context.Background(), "node-1", "/data"); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("ListFiles() error = %v", err)
	}
}

// dockerCallLog 用于在 mock docker daemon 中记录每个收到的请求。
type dockerCallLog struct {
	method string
	path   string
	body   []byte
}

// startMockDocker 模拟 docker daemon 处理 ContainerCreate / ContainerInspect 两个端点。
// fixedID 用于 create 响应；inspectStatus 用于 inspect 时返回的 State.Status。
func startMockDocker(t *testing.T, fixedID, inspectStatus string, calls *[]dockerCallLog) (*httptest.Server, *client.Client) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*calls = append(*calls, dockerCallLog{method: r.Method, path: r.URL.Path, body: body})
		w.Header().Set("Api-Version", "1.41")
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/create"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": fixedID, "Warnings": []string{}})
		case strings.HasSuffix(r.URL.Path, "/json"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id":    fixedID,
				"Name":  "/" + fixedID,
				"Image": "openclaw-runtime:dev",
				"State": map[string]any{"Status": inspectStatus},
			})
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("意外请求 path = %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithHTTPClient(server.Client()), client.WithVersion("1.41"))
	if err != nil {
		t.Fatalf("构造 mock docker client: %v", err)
	}
	return server, cli
}

type staticDockerResolver struct {
	cli *client.Client
}

func (s *staticDockerResolver) DockerClient(_ context.Context, _ string) (*client.Client, error) {
	return s.cli, nil
}

func TestAgentBackedAdapterCreateContainerHappyPath(t *testing.T) {
	var calls []dockerCallLog
	_, cli := startMockDocker(t, "ctr-1", "created", &calls)
	adapter := NewAgentBackedAdapter(nil, &staticDockerResolver{cli: cli}, nil)

	spec := ContainerSpec{
		Name:  "ocm-app-x",
		Image: "openclaw-runtime:dev",
		Env:   map[string]string{"OPENCLAW_API_KEY": "k1", "OPENCLAW_API_BASE": "http://newapi"},
		Volumes: []VolumeMount{
			{HostPath: "/data/workspace", ContainerPath: "/workspace"},
			{HostPath: "/data/knowledge/org", ContainerPath: "/knowledge/org", ReadOnly: true},
		},
		Networks:  []string{"oc-net"},
		Resources: Resources{CPULimit: 2000, MemoryBytes: 1 << 30},
		Command:   []string{"openclaw", "run"},
	}

	info, err := adapter.CreateContainer(context.Background(), "node-1", spec)
	if err != nil {
		t.Fatalf("CreateContainer err = %v", err)
	}
	if info.ID != "ctr-1" {
		t.Fatalf("info.ID = %q, want ctr-1", info.ID)
	}
	if info.Status != "created" {
		t.Fatalf("info.Status = %q, want created", info.Status)
	}
	if info.Name != "ctr-1" {
		t.Fatalf("info.Name = %q, want ctr-1（应去掉前导 /）", info.Name)
	}

	createCall := findCall(t, calls, "POST", "/containers/create")
	body := string(createCall.body)
	for _, fragment := range []string{
		`"Image":"openclaw-runtime:dev"`,
		`"OPENCLAW_API_BASE=http://newapi"`,
		`"OPENCLAW_API_KEY=k1"`,
		`"openclaw","run"`,
		`"/data/workspace:/workspace"`,
		`"/data/knowledge/org:/knowledge/org:ro"`,
	} {
		if !strings.Contains(body, fragment) {
			t.Errorf("create body 缺片段 %s\n实际 body=%s", fragment, body)
		}
	}
	if !strings.Contains(body, `"NanoCpus":2000000000`) {
		t.Errorf("CPU 限额未正确翻译: %s", body)
	}
	if !strings.Contains(body, `"Memory":1073741824`) {
		t.Errorf("Memory 限额未正确翻译: %s", body)
	}
}

func TestAgentBackedAdapterCreateContainerFailsWithoutResolver(t *testing.T) {
	adapter := NewAgentBackedAdapter(nil, nil, nil)
	if _, err := adapter.CreateContainer(context.Background(), "n", ContainerSpec{}); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("没有 docker resolver 时 err = %v, want ErrUnimplemented", err)
	}
}

func TestAgentBackedAdapterInspectContainer(t *testing.T) {
	var calls []dockerCallLog
	_, cli := startMockDocker(t, "ctr-2", "running", &calls)
	adapter := NewAgentBackedAdapter(nil, &staticDockerResolver{cli: cli}, nil)
	info, err := adapter.InspectContainer(context.Background(), "node", "ctr-2")
	if err != nil {
		t.Fatalf("InspectContainer err = %v", err)
	}
	if info.ID != "ctr-2" || info.Status != "running" {
		t.Fatalf("info = %+v", info)
	}
	if findCall(t, calls, "GET", "/containers/ctr-2/json").path == "" {
		t.Fatal("没有触发 inspect 请求")
	}
}

func findCall(t *testing.T, calls []dockerCallLog, method, suffix string) dockerCallLog {
	t.Helper()
	for _, c := range calls {
		if c.method == method && strings.HasSuffix(c.path, suffix) {
			return c
		}
	}
	t.Fatalf("没找到 %s %s 调用，全部=%+v", method, suffix, calls)
	return dockerCallLog{}
}

type fakeImageSyncer struct {
	result    imagesync.SyncResult
	err       error
	lastImage string
	lastNode  string
}

func (f *fakeImageSyncer) SyncOpenClawImage(_ context.Context, nodeID, image string) (imagesync.SyncResult, error) {
	f.lastNode = nodeID
	f.lastImage = image
	return f.result, f.err
}

type fakeResolverFn func(ctx context.Context, nodeID string) (*agent.AgentFileClient, error)

func (f fakeResolverFn) FileClient(ctx context.Context, nodeID string) (*agent.AgentFileClient, error) {
	return f(ctx, nodeID)
}
