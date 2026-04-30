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

func TestAgentBackedAdapterContainerOpsRequireDockerResolver(t *testing.T) {
	// 没有 docker resolver 时所有容器接口都退化为 ErrUnimplemented，让上层快速识别装配缺失。
	adapter := NewAgentBackedAdapter(nil, nil, nil)
	if _, err := adapter.CreateContainer(context.Background(), "n1", ContainerSpec{}); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("CreateContainer err = %v", err)
	}
	if err := adapter.StartContainer(context.Background(), "n1", "c1"); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("StartContainer err = %v", err)
	}
	if err := adapter.StopContainer(context.Background(), "n1", "c1"); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("StopContainer err = %v", err)
	}
	if err := adapter.RestartContainer(context.Background(), "n1", "c1"); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("RestartContainer err = %v", err)
	}
	if err := adapter.RemoveContainer(context.Background(), "n1", "c1"); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("RemoveContainer err = %v", err)
	}
	if _, err := adapter.InspectContainer(context.Background(), "n1", "c1"); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("InspectContainer err = %v", err)
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

// startMockDockerLifecycle 在 startMockDocker 基础上额外处理 start/stop/restart/remove 端点。
// 用法上保持兼容：传入 calls 收集请求，可选 errOn 让指定 op 返回 5xx 模拟 docker 故障。
func startMockDockerLifecycle(t *testing.T, calls *[]dockerCallLog, errOn map[string]int) (*httptest.Server, *client.Client) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*calls = append(*calls, dockerCallLog{method: r.Method, path: r.URL.RequestURI(), body: body})
		w.Header().Set("Api-Version", "1.41")
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/start"):
			if c := errOn["start"]; c != 0 {
				w.WriteHeader(c)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(path, "/stop"):
			if c := errOn["stop"]; c != 0 {
				w.WriteHeader(c)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(path, "/restart"):
			if c := errOn["restart"]; c != 0 {
				w.WriteHeader(c)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete:
			if c := errOn["remove"]; c != 0 {
				w.WriteHeader(c)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(path, "/_ping"):
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("意外请求 path = %s", path)
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

func TestAgentBackedAdapterStartStopRestartRemove_HappyPath(t *testing.T) {
	var calls []dockerCallLog
	_, cli := startMockDockerLifecycle(t, &calls, nil)
	adapter := NewAgentBackedAdapter(nil, &staticDockerResolver{cli: cli}, nil)

	if err := adapter.StartContainer(context.Background(), "n", "ctr-1"); err != nil {
		t.Fatalf("StartContainer err = %v", err)
	}
	if err := adapter.StopContainer(context.Background(), "n", "ctr-1"); err != nil {
		t.Fatalf("StopContainer err = %v", err)
	}
	if err := adapter.RestartContainer(context.Background(), "n", "ctr-1"); err != nil {
		t.Fatalf("RestartContainer err = %v", err)
	}
	if err := adapter.RemoveContainer(context.Background(), "n", "ctr-1"); err != nil {
		t.Fatalf("RemoveContainer err = %v", err)
	}

	for _, suffix := range []string{"/start", "/stop", "/restart"} {
		findCall(t, calls, http.MethodPost, suffix)
	}
	findCall(t, calls, http.MethodDelete, "/containers/ctr-1")
}

func TestAgentBackedAdapterStartContainerPropagatesDockerError(t *testing.T) {
	var calls []dockerCallLog
	_, cli := startMockDockerLifecycle(t, &calls, map[string]int{"start": http.StatusInternalServerError})
	adapter := NewAgentBackedAdapter(nil, &staticDockerResolver{cli: cli}, nil)
	err := adapter.StartContainer(context.Background(), "n", "ctr-x")
	if err == nil {
		t.Fatal("docker 5xx 时应冒泡错误")
	}
	if !strings.Contains(err.Error(), "启动容器失败") {
		t.Fatalf("错误信息缺中文上下文: %v", err)
	}
}

func TestAgentBackedAdapterStopContainerSetsTimeout(t *testing.T) {
	var calls []dockerCallLog
	_, cli := startMockDockerLifecycle(t, &calls, nil)
	adapter := NewAgentBackedAdapter(nil, &staticDockerResolver{cli: cli}, nil)
	if err := adapter.StopContainer(context.Background(), "n", "ctr-1"); err != nil {
		t.Fatalf("StopContainer err = %v", err)
	}
	stop := findCall(t, calls, http.MethodPost, "/stop")
	if !strings.Contains(stop.path, "t=30") {
		t.Fatalf("stop 调用未携带 t=30 timeout: path=%s", stop.path)
	}
}

func TestAgentBackedAdapterRemoveContainerForcesDeletion(t *testing.T) {
	var calls []dockerCallLog
	_, cli := startMockDockerLifecycle(t, &calls, nil)
	adapter := NewAgentBackedAdapter(nil, &staticDockerResolver{cli: cli}, nil)
	if err := adapter.RemoveContainer(context.Background(), "n", "ctr-1"); err != nil {
		t.Fatalf("RemoveContainer err = %v", err)
	}
	remove := findCall(t, calls, http.MethodDelete, "/containers/ctr-1")
	if !strings.Contains(remove.path, "force=1") {
		t.Fatalf("remove 调用未带 force=1: %s", remove.path)
	}
}

// startMockDocker 模拟 docker daemon 处理 ContainerCreate / ContainerInspect 两个端点。
// fixedID 用于 create 响应；inspectStatus 用于 inspect 时返回的 State.Status。
func startMockDocker(t *testing.T, fixedID, inspectStatus string, calls *[]dockerCallLog) (*httptest.Server, *client.Client) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*calls = append(*calls, dockerCallLog{method: r.Method, path: r.URL.RequestURI(), body: body})
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

// findCall 在 calls 列表里查找 method 一致且 path 包含给定子串的调用。
// path 字段记录的是 RequestURI（含 query），这里用 Contains 兼容 /start?t=30 之类 query。
func findCall(t *testing.T, calls []dockerCallLog, method, fragment string) dockerCallLog {
	t.Helper()
	for _, c := range calls {
		if c.method == method && strings.Contains(c.path, fragment) {
			return c
		}
	}
	t.Fatalf("没找到 %s %s 调用，全部=%+v", method, fragment, calls)
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
