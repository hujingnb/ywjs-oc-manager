package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oc-manager/internal/integrations/agent"
)

// TestAgentBackedAdapterContainerOpsRequireDockerResolver 验证agentBacked适配器容器操作RequireDocker解析器的预期行为场景。
func TestAgentBackedAdapterContainerOpsRequireDockerResolver(t *testing.T) {
	// 没有 docker resolver 时所有容器接口都退化为 ErrUnimplemented，让上层快速识别装配缺失。
	adapter := NewAgentBackedAdapter(nil, nil)
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

// TestAgentBackedAdapterFileOpsRouteThroughAgent 验证agentBacked适配器文件操作路由通过 agent的预期行为场景。
func TestAgentBackedAdapterFileOpsRouteThroughAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/files/list":
			_, _ = w.Write([]byte(`{"path":"/data","entries":[]}`))
		case "/v1/files/get":
			_, _ = w.Write([]byte("payload"))
		case "/v1/files/put":
			body, _ := io.ReadAll(r.Body)
			assert.Equal(t, "hello", string(body))
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
		require.Equal(t, "node-1", nodeID)
		return agent.NewFileClient(server.URL, ""), nil
	})
	adapter := NewAgentBackedAdapter(resolver, nil)

	_, err := adapter.ListFiles(context.Background(), "node-1", "/data")
	require.NoError(t, err)
	err = adapter.UploadFile(context.Background(), "node-1", "/data/x.txt", strings.NewReader("hello"))
	require.NoError(t, err)
	stream, err := adapter.DownloadFile(context.Background(), "node-1", "/data/x.txt")
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, stream)
	require.NoError(t, err)
	stream.Close()
	err = adapter.DeletePath(context.Background(), "node-1", "/data/x.txt")
	require.NoError(t, err)
	tar, err := adapter.ArchiveDirectory(context.Background(), "node-1", "/data")
	require.NoError(t, err)
	tarBody, _ := io.ReadAll(tar)
	tar.Close()
	require.Equal(t, "tar-bytes", string(tarBody))
}

// TestAgentBackedAdapterFileOpsRequireResolver 验证agentBacked适配器文件操作Require解析器的预期行为场景。
func TestAgentBackedAdapterFileOpsRequireResolver(t *testing.T) {
	adapter := NewAgentBackedAdapter(nil, nil)
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
	require.NoError(t, err)
	return server, cli
}

// TestAgentBackedAdapterStartStopRestartRemove_HappyPath 验证agentBacked适配器启动停止重启移除成功路径的成功路径场景。
func TestAgentBackedAdapterStartStopRestartRemove_HappyPath(t *testing.T) {
	var calls []dockerCallLog
	_, cli := startMockDockerLifecycle(t, &calls, nil)
	adapter := NewAgentBackedAdapter(nil, &staticDockerResolver{cli: cli})

	err := adapter.StartContainer(context.Background(), "n", "ctr-1")
	require.NoError(t, err)
	err = adapter.StopContainer(context.Background(), "n", "ctr-1")
	require.NoError(t, err)
	err = adapter.RestartContainer(context.Background(), "n", "ctr-1")
	require.NoError(t, err)
	err = adapter.RemoveContainer(context.Background(), "n", "ctr-1")
	require.NoError(t, err)

	for _, suffix := range []string{"/start", "/stop", "/restart"} {
		findCall(t, calls, http.MethodPost, suffix)
	}
	findCall(t, calls, http.MethodDelete, "/containers/ctr-1")
}

// TestAgentBackedAdapterStartContainerPropagatesDockerError 验证agentBacked适配器启动容器透传Docker错误的错误映射或错误记录场景。
func TestAgentBackedAdapterStartContainerPropagatesDockerError(t *testing.T) {
	var calls []dockerCallLog
	_, cli := startMockDockerLifecycle(t, &calls, map[string]int{"start": http.StatusInternalServerError})
	adapter := NewAgentBackedAdapter(nil, &staticDockerResolver{cli: cli})
	err := adapter.StartContainer(context.Background(), "n", "ctr-x")
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "启动容器失败"))
}

// TestAgentBackedAdapterStopContainerSetsTimeout 验证agentBacked适配器停止容器Sets超时的预期行为场景。
func TestAgentBackedAdapterStopContainerSetsTimeout(t *testing.T) {
	var calls []dockerCallLog
	_, cli := startMockDockerLifecycle(t, &calls, nil)
	adapter := NewAgentBackedAdapter(nil, &staticDockerResolver{cli: cli})
	err := adapter.StopContainer(context.Background(), "n", "ctr-1")
	require.NoError(t, err)
	stop := findCall(t, calls, http.MethodPost, "/stop")
	require.True(t, strings.Contains(stop.path, "t=30"))
}

// TestAgentBackedAdapterRemoveContainerForcesDeletion 验证agentBacked适配器移除容器针对cesDeletion的预期行为场景。
func TestAgentBackedAdapterRemoveContainerForcesDeletion(t *testing.T) {
	var calls []dockerCallLog
	_, cli := startMockDockerLifecycle(t, &calls, nil)
	adapter := NewAgentBackedAdapter(nil, &staticDockerResolver{cli: cli})
	err := adapter.RemoveContainer(context.Background(), "n", "ctr-1")
	require.NoError(t, err)
	remove := findCall(t, calls, http.MethodDelete, "/containers/ctr-1")
	require.True(t, strings.Contains(remove.path, "force=1"))
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
				"Image": "hermes-runtime:dev",
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
	require.NoError(t, err)
	return server, cli
}

type staticDockerResolver struct {
	cli *client.Client
}

func (s *staticDockerResolver) DockerClient(_ context.Context, _ string) (*client.Client, error) {
	return s.cli, nil
}

// TestAgentBackedAdapterCreateContainerHappyPath 验证agentBacked适配器创建容器成功路径的成功路径场景。
func TestAgentBackedAdapterCreateContainerHappyPath(t *testing.T) {
	var calls []dockerCallLog
	_, cli := startMockDocker(t, "ctr-1", "created", &calls)
	adapter := NewAgentBackedAdapter(nil, &staticDockerResolver{cli: cli})

	spec := ContainerSpec{
		Name:  "ocm-app-x",
		Image: "hermes-runtime:dev",
		Env:   map[string]string{"OPENAI_API_KEY": "k1", "OPENAI_BASE_URL": "http://newapi"},
		Volumes: []VolumeMount{
			{HostPath: "/data/workspace", ContainerPath: "/workspace"},                         // 场景：工作区挂载应以读写 bind mount 传给 Docker。
			{HostPath: "/data/knowledge/org", ContainerPath: "/knowledge/org", ReadOnly: true}, // 场景：组织知识库挂载应以只读 bind mount 传给 Docker。
		},
		Networks:  []string{"oc-net"},
		Resources: Resources{CPULimit: 2000, MemoryBytes: 1 << 30},
		Command:   []string{"hermes", "start"},
	}

	info, err := adapter.CreateContainer(context.Background(), "node-1", spec)
	require.NoError(t, err)
	require.Equal(t, "ctr-1", info.ID)
	require.Equal(t, "created", info.Status)
	require.Equal(t, "ctr-1", info.Name)

	createCall := findCall(t, calls, "POST", "/containers/create")
	body := string(createCall.body)
	for _, fragment := range []string{
		`"Image":"hermes-runtime:dev"`,
		`"OPENAI_BASE_URL=http://newapi"`,
		`"OPENAI_API_KEY=k1"`,
		`"hermes","start"`,
		`"/data/workspace:/workspace"`,
		`"/data/knowledge/org:/knowledge/org:ro"`,
	} {
		assert.Contains(t, body, fragment)
	}
	assert.Contains(t, body, `"NanoCpus":2000000000`)
	assert.Contains(t, body, `"Memory":1073741824`)
}

// TestAgentBackedAdapterCreateContainerFailsWithoutResolver 验证agentBacked适配器创建容器失败不使用解析器的预期行为场景。
func TestAgentBackedAdapterCreateContainerFailsWithoutResolver(t *testing.T) {
	adapter := NewAgentBackedAdapter(nil, nil)
	if _, err := adapter.CreateContainer(context.Background(), "n", ContainerSpec{}); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("没有 docker resolver 时 err = %v, want ErrUnimplemented", err)
	}
}

// TestAgentBackedAdapterInspectContainer 验证agentBacked适配器检查容器的预期行为场景。
func TestAgentBackedAdapterInspectContainer(t *testing.T) {
	var calls []dockerCallLog
	_, cli := startMockDocker(t, "ctr-2", "running", &calls)
	adapter := NewAgentBackedAdapter(nil, &staticDockerResolver{cli: cli})
	info, err := adapter.InspectContainer(context.Background(), "node", "ctr-2")
	require.NoError(t, err)
	if info.ID != "ctr-2" || info.Status != "running" {
		t.Fatalf("info = %+v", info)
	}
	require.NotEqual(t, "", findCall(t, calls, "GET", "/containers/ctr-2/json").path)
}

// TestStatsResponseToContainerStatsSumsBlockIO 验证容器stats会汇总Docker块设备读写累计字节。
func TestStatsResponseToContainerStatsSumsBlockIO(t *testing.T) {
	got := statsResponseToContainerStats(container.StatsResponse{
		Stats: container.Stats{
			BlkioStats: container.BlkioStats{IoServiceBytesRecursive: []container.BlkioStatEntry{
				{Op: "Read", Value: 100},  // 场景：读字节来自Docker Read条目。
				{Op: "read", Value: 25},   // 场景：大小写差异不应影响读字节累计。
				{Op: "Write", Value: 300}, // 场景：写字节来自Docker Write条目。
				{Op: "Sync", Value: 999},  // 场景：非读写条目不能混入磁盘读写展示。
			}},
		},
	})

	assert.Equal(t, uint64(125), got.DiskReadBytes)
	assert.Equal(t, uint64(300), got.DiskWriteBytes)
}

// TestStatsResponseToContainerStatsAllowsMissingBlockIO 验证缺失块设备指标时保持零值且不产生错误状态。
func TestStatsResponseToContainerStatsAllowsMissingBlockIO(t *testing.T) {
	got := statsResponseToContainerStats(container.StatsResponse{})

	assert.Equal(t, uint64(0), got.DiskReadBytes)
	assert.Equal(t, uint64(0), got.DiskWriteBytes)
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

type fakeResolverFn func(ctx context.Context, nodeID string) (*agent.AgentFileClient, error)

func (f fakeResolverFn) FileClient(ctx context.Context, nodeID string) (*agent.AgentFileClient, error) {
	return f(ctx, nodeID)
}

// fakeInspector 按顺序返回预设的 ContainerInfo 序列；序列耗尽后返回最后一个值（模拟稳定状态）。
// 用于 WaitContainerHealthy 单测注入，避免依赖真实 docker daemon。
type fakeInspector struct {
	seq []ContainerInfo
	idx int32
}

func (f *fakeInspector) InspectContainer(_ context.Context, _, _ string) (ContainerInfo, error) {
	i := atomic.AddInt32(&f.idx, 1) - 1
	if int(i) >= len(f.seq) {
		return f.seq[len(f.seq)-1], nil
	}
	return f.seq[i], nil
}

// TestWaitContainerHealthy_StartingThenHealthy 覆盖容器先 starting 后 healthy 的常规路径。
// Hermes 启动后 HEALTHCHECK 先报 starting，数轮后报 healthy，WaitContainerHealthy 应返回 nil。
func TestWaitContainerHealthy_StartingThenHealthy(t *testing.T) {
	insp := &fakeInspector{seq: []ContainerInfo{
		{Health: ContainerHealth{Status: "starting"}},
		{Health: ContainerHealth{Status: "starting"}},
		{Health: ContainerHealth{Status: "healthy"}},
	}}
	a := &AgentBackedAdapter{inspector: insp}
	err := a.WaitContainerHealthy(context.Background(), "node1", "cont1", 30*time.Second)
	require.NoError(t, err)
}

// TestWaitContainerHealthy_UnhealthyFailsFast 覆盖容器报 unhealthy 时快速失败，不再等 timeout。
// Output 字段应透传到错误信息，便于排障。
func TestWaitContainerHealthy_UnhealthyFailsFast(t *testing.T) {
	insp := &fakeInspector{seq: []ContainerInfo{
		{Health: ContainerHealth{Status: "unhealthy", Output: "boom"}},
	}}
	a := &AgentBackedAdapter{inspector: insp}
	err := a.WaitContainerHealthy(context.Background(), "node1", "cont1", 30*time.Second)
	require.Error(t, err)
	require.Contains(t, err.Error(), "boom")
}
