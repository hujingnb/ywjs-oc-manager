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

type LocalDockerCLIProvider struct {
	Command string
}

func (p LocalDockerCLIProvider) dockerCommand() string {
	if p.Command == "" {
		return "docker"
	}
	return p.Command
}

func (p LocalDockerCLIProvider) ImageID(ctx context.Context, image string) (string, error) {
	cmd := exec.CommandContext(ctx, p.dockerCommand(), "image", "inspect", image, "--format", "{{.Id}}")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("docker image inspect %s: %w", image, err)
	}
	return strings.TrimSpace(string(output)), nil
}

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

type commandReadCloser struct {
	io.ReadCloser
	wait   func() error
	stderr *bytes.Buffer
}

func (c *commandReadCloser) Close() error {
	closeErr := c.ReadCloser.Close()
	waitErr := c.wait()
	if waitErr != nil {
		return fmt.Errorf("docker save failed: %w: %s", waitErr, c.stderr.String())
	}
	return closeErr
}

type AgentHTTPClient struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

func (c AgentHTTPClient) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

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

func (c AgentHTTPClient) authorize(req *http.Request) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
}
