package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog" // todo del
	"net"
	"net/http"
	"net/url"
	"strings"
)

var ErrImageNotFound = errors.New("docker image not found")

// DockerClient 封装 agent 对本机 Docker Engine 的最小依赖。
// manager 只通过 agent 间接访问 Docker，单元测试也可以用 fake 实现替代真实 socket。
type DockerClient interface {
	InspectImage(ctx context.Context, image string) (DockerImageInfo, error)
	LoadImage(ctx context.Context, archive io.Reader) error
	// ListContainers 按容器名前缀统计本节点实例数；当前用于统计 ocm-* 应用容器。
	ListContainers(ctx context.Context, namePrefix string) (int32, error)
}

// DockerImageInfo 是 manager 判断镜像一致性所需的最小元数据。
// ID 是 Docker image config digest，适合和 manager 本地构建出的 image ID 做精确比对。
type DockerImageInfo struct {
	ID          string   `json:"id"`
	RepoTags    []string `json:"repoTags"`
	RepoDigests []string `json:"repoDigests"`
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

func (c *dockerSocketClient) InspectImage(ctx context.Context, image string) (DockerImageInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/images/"+url.PathEscape(image)+"/json", nil)
	if err != nil {
		return DockerImageInfo{}, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return DockerImageInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return DockerImageInfo{}, ErrImageNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return DockerImageInfo{}, fmt.Errorf("docker inspect image failed: status=%d body=%s", resp.StatusCode, string(body))
	}

	var payload struct {
		ID          string   `json:"Id"`
		RepoTags    []string `json:"RepoTags"`
		RepoDigests []string `json:"RepoDigests"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return DockerImageInfo{}, err
	}
	return DockerImageInfo{
		ID:          payload.ID,
		RepoTags:    payload.RepoTags,
		RepoDigests: payload.RepoDigests,
	}, nil
}

func (c *dockerSocketClient) LoadImage(ctx context.Context, archive io.Reader) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://docker/images/load?quiet=1", archive)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-tar")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("docker load image failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	dockerLoadResp, _ := io.ReadAll(io.LimitReader(resp.Body, 8192)) // todo del origin: _, _ = io.Copy(io.Discard, resp.Body)
	slog.Error("[hujingnb][D] dockerSocketClient:LoadImage docker daemon response", "body", string(dockerLoadResp)) // todo del
	return nil
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
