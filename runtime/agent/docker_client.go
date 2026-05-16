package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// DockerClient 封装 agent 对本机 Docker Engine 的最小依赖。
// manager 只通过 agent 间接访问 Docker，单元测试也可以用 fake 实现替代真实 socket。
type DockerClient interface {
	// ListContainers 按容器名前缀统计本节点实例数；当前用于统计 ocm-* 应用容器。
	ListContainers(ctx context.Context, namePrefix string) (int32, error)
}

type dockerSocketClient struct {
	httpClient *http.Client
}

func newDockerSocketClient(socketPath string) DockerClient {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// Docker remote API 走 HTTP 语义，但本地 agent 通过 Unix socket 零侵入转发到宿主机 Docker。
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	return &dockerSocketClient{httpClient: &http.Client{Transport: transport}}
}

// ListContainers 通过 Docker Remote API 拉取容器列表，并按 Docker 返回的 /name 格式做前缀匹配。
func (c *dockerSocketClient) ListContainers(ctx context.Context, namePrefix string) (int32, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/containers/json", nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, fmt.Errorf("docker list containers failed: status=%d body=%s", resp.StatusCode, string(body))
	}

	var payload []struct {
		Names []string `json:"Names"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, err
	}
	var count int32
	for _, container := range payload {
		for _, name := range container.Names {
			if strings.HasPrefix(strings.TrimPrefix(name, "/"), namePrefix) {
				count++
				break
			}
		}
	}
	return count, nil
}
