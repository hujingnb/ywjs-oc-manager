package imagesync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
)

// LocalDockerCLIProvider 通过本机 docker CLI inspect/save 镜像。
// 这是 manager 侧镜像源，Command 仅用于测试或非标准 docker 二进制路径。
type LocalDockerCLIProvider struct {
	Command string
}

// dockerCommand 返回实际执行的 docker 命令名；空值保持生产默认 "docker"。
func (p LocalDockerCLIProvider) dockerCommand() string {
	if p.Command == "" {
		return "docker"
	}
	return p.Command
}

// ImageID 读取本地镜像 ID，用于和目标节点返回的 ID 做精确比对。
func (p LocalDockerCLIProvider) ImageID(ctx context.Context, image string) (string, error) {
	cmd := exec.CommandContext(ctx, p.dockerCommand(), "image", "inspect", image, "--format", "{{.Id}}")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("docker image inspect %s: %w", image, err)
	}
	return strings.TrimSpace(string(output)), nil
}

// Archive 通过 docker save 生成镜像 tar 流。
// 调用方必须 Close 返回值，否则底层 docker 子进程可能无法回收。
func (p LocalDockerCLIProvider) Archive(ctx context.Context, image string) (io.ReadCloser, error) {
	// docker save 可能输出很大的 tar 包，这里保持流式读取，避免 manager 把整份镜像压到内存。
	cmd := exec.CommandContext(ctx, p.dockerCommand(), "save", image)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &commandReadCloser{ReadCloser: stdout, wait: cmd.Wait, stderr: &stderr}, nil
}

// commandReadCloser 把 docker save stdout 与 cmd.Wait 绑定到同一个 Close 调用。
// 这样读流成功但 docker 进程最终失败时，调用方仍能在 Close 阶段拿到 stderr。
type commandReadCloser struct {
	io.ReadCloser
	wait   func() error
	stderr *bytes.Buffer
}

// Close 关闭 stdout 并等待 docker save 退出。
func (c *commandReadCloser) Close() error {
	closeErr := c.ReadCloser.Close()
	waitErr := c.wait()
	if waitErr != nil {
		return fmt.Errorf("docker save failed: %w: %s", waitErr, c.stderr.String())
	}
	return closeErr
}

// AgentHTTPClient 调用 runtime agent 的镜像接口。
// BaseURL 必须指向单个节点 agent；nodeID 参数只用于满足接口形态，不在 URL 内再次拼接。
type AgentHTTPClient struct {
	BaseURL string
	// Token 为空表示本地调试模式不加 Authorization；生产装配必须提供 agent token。
	Token string
	// HTTPClient 承载 TLS、超时和 transport 设置；nil 时退回 http.DefaultClient。
	HTTPClient *http.Client
}

// httpClient 返回实际 HTTP client，允许测试注入 httptest 的自定义 transport。
func (c AgentHTTPClient) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

// InspectImage 调 agent GET /v1/images/inspect?image=... 查询目标节点镜像状态。
func (c AgentHTTPClient) InspectImage(ctx context.Context, _ string, image string) (RemoteImageInfo, error) {
	endpoint, err := url.JoinPath(c.BaseURL, "/v1/images/inspect")
	if err != nil {
		return RemoteImageInfo{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?image="+url.QueryEscape(image), nil)
	if err != nil {
		return RemoteImageInfo{}, err
	}
	c.authorize(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return RemoteImageInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// agent 错误响应只截取前 4KB，保留诊断信息同时避免异常 body 撑爆 worker 日志。
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return RemoteImageInfo{}, fmt.Errorf("inspect agent image failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	var payload struct {
		Exists bool `json:"exists"`
		Info   struct {
			ID string `json:"id"`
		} `json:"info"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return RemoteImageInfo{}, err
	}
	return RemoteImageInfo{Exists: payload.Exists, ID: payload.Info.ID}, nil
}

// LoadImage 调 agent POST /v1/images/load，把 docker save tar 流加载到目标节点。
// archive 由调用方提供，函数不会重试读取；失败重试由 worker 重新创建 archive。
func (c AgentHTTPClient) LoadImage(ctx context.Context, _ string, image string, archive io.Reader) (RemoteImageInfo, error) {
	endpoint, err := url.JoinPath(c.BaseURL, "/v1/images/load")
	if err != nil {
		return RemoteImageInfo{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"?image="+url.QueryEscape(image), archive)
	if err != nil {
		return RemoteImageInfo{}, err
	}
	req.Header.Set("Content-Type", "application/x-tar")
	c.authorize(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return RemoteImageInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// load 失败一般来自 agent 鉴权、docker daemon 或 tar 格式错误，body 保留前 4KB 方便定位。
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return RemoteImageInfo{}, fmt.Errorf("load agent image failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	var payload struct {
		Info struct {
			ID string `json:"id"`
		} `json:"info"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return RemoteImageInfo{}, err
	}
	return RemoteImageInfo{Exists: true, ID: payload.Info.ID}, nil
}

// authorize 按 agent 协议写入 Bearer token。
// 空 token 不写头是为了兼容本地无鉴权 agent，不能理解为生产默认安全配置。
func (c AgentHTTPClient) authorize(req *http.Request) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
}
