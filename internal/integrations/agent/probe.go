package agent

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ProbeResult 是 manager 主动探测 runtime agent 双端口的标准化结果。
type ProbeResult struct {
	OK    bool
	Error string
}

// ProbeClient 通过 agent 自签 CA 与 agent token 探测 docker/file 两个入站端口。
type ProbeClient struct {
	Timeout time.Duration
}

// NewProbeClient 创建 agent 探测客户端。
func NewProbeClient(timeout time.Duration) *ProbeClient {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &ProbeClient{Timeout: timeout}
}

// Probe 依次探测 docker proxy 的 _ping 与 file API 的 /v1/files/ping。
func (c *ProbeClient) Probe(ctx context.Context, dockerEndpoint, fileEndpoint, token, caCertPEM string) ProbeResult {
	httpClient, err := c.httpClient(caCertPEM)
	if err != nil {
		return ProbeResult{OK: false, Error: "tls_ca_invalid: " + err.Error()}
	}
	if err := probeURL(ctx, httpClient, dockerEndpoint, "/v1/docker/_ping", token); err != nil {
		return ProbeResult{OK: false, Error: "docker_ping_failed: " + err.Error()}
	}
	if err := probeURL(ctx, httpClient, fileEndpoint, "/v1/files/ping", token); err != nil {
		return ProbeResult{OK: false, Error: "file_ping_failed: " + err.Error()}
	}
	return ProbeResult{OK: true}
}

func (c *ProbeClient) httpClient(caCertPEM string) (*http.Client, error) {
	pool, err := buildCertPool(caCertPEM)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout: c.Timeout,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs:    pool,
			MinVersion: tls.VersionTLS12,
		}},
	}, nil
}

func probeURL(ctx context.Context, client *http.Client, baseURL, p, token string) error {
	if strings.TrimSpace(baseURL) == "" {
		return fmt.Errorf("endpoint 为空")
	}
	endpoint, err := url.JoinPath(baseURL, p)
	if err != nil {
		return fmt.Errorf("拼接 endpoint 失败: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("构造请求失败: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
