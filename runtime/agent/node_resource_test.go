package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNodeResourceCollectorParsesLinuxProcFiles 验证节点资源采集器能解析 Linux /proc 文本的正常路径。
func TestNodeResourceCollectorParsesLinuxProcFiles(t *testing.T) {
	dir := t.TempDir()
	statPath := filepath.Join(dir, "stat")
	memInfoPath := filepath.Join(dir, "meminfo")
	netDevPath := filepath.Join(dir, "net_dev")

	require.NoError(t, os.WriteFile(statPath, []byte("cpu  100 20 30 400 50 6 7 0 0 0\n"), 0o600))
	require.NoError(t, os.WriteFile(memInfoPath, []byte("MemTotal:       1024000 kB\nMemFree:         100000 kB\nBuffers:          20000 kB\nCached:          300000 kB\nSReclaimable:     40000 kB\nShmem:            10000 kB\n"), 0o600))
	require.NoError(t, os.WriteFile(netDevPath, []byte("Inter-|   Receive                                                |  Transmit\n face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n  lo: 1000 1 0 0 0 0 0 0 2000 1 0 0 0 0 0 0\neth0: 3000 2 0 0 0 0 0 0 4000 2 0 0 0 0 0 0\n"), 0o600))

	statRaw, err := os.ReadFile(statPath)
	require.NoError(t, err)
	memInfoRaw, err := os.ReadFile(memInfoPath)
	require.NoError(t, err)
	netDevRaw, err := os.ReadFile(netDevPath)
	require.NoError(t, err)

	idle, total, err := parseProcStatCPU(string(statRaw))
	require.NoError(t, err)
	assert.Equal(t, uint64(450), idle)
	assert.Equal(t, uint64(613), total)

	used, totalMemory, err := parseMemInfo(string(memInfoRaw))
	require.NoError(t, err)
	assert.Equal(t, int64(587776000), used)
	assert.Equal(t, int64(1048576000), totalMemory)

	rx, tx, err := parseNetDev(string(netDevRaw))
	require.NoError(t, err)
	assert.Equal(t, int64(4000), rx)
	assert.Equal(t, int64(6000), tx)
}

// TestCollectNodeResourceTimesOutDockerSampling 验证 Docker 实例数采样卡住时不会阻塞节点资源采集。
func TestCollectNodeResourceTimesOutDockerSampling(t *testing.T) {
	docker := &blockingDockerClient{}

	start := time.Now()
	snapshot, _ := collectNodeResource(t.TempDir(), docker, nil)

	require.Less(t, time.Since(start), 2*time.Second)
	require.ErrorIs(t, docker.err, context.DeadlineExceeded)
	assert.Nil(t, snapshot.InstanceCount)
	assert.Contains(t, snapshot.LastError, "docker:")
}

// TestDockerSocketClientListContainersCountsActiveManagedContainers 验证实例数只按 Docker 当前活跃容器列表统计 ocm-* 容器。
func TestDockerSocketClientListContainersCountsActiveManagedContainers(t *testing.T) {
	client := &dockerSocketClient{httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		assert.Equal(t, "/containers/json", req.URL.Path)
		assert.NotContains(t, req.URL.RawQuery, "all=1")

		body := `[{"Names":["/ocm-active"]},{"Names":["/redis"]}]`
		if strings.Contains(req.URL.RawQuery, "all=1") {
			body = `[{"Names":["/ocm-active"]},{"Names":["/ocm-stopped"],"State":"exited"}]`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}}

	count, err := client.ListContainers(context.Background(), "ocm-")

	require.NoError(t, err)
	assert.Equal(t, int32(1), count)
}

type blockingDockerClient struct {
	err error
}

func (f *blockingDockerClient) ListContainers(ctx context.Context, _ string) (int32, error) {
	<-ctx.Done()
	f.err = ctx.Err()
	return 0, f.err
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
