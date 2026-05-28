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
	// OK 表示 docker 代理和文件 API 两个端点都探测成功。
	OK bool
	// Error 是给管理端展示的稳定失败原因前缀，包含 tls_ca_invalid/docker_ping_failed/file_ping_failed。
	Error string
}

// ProbeClient 通过 agent 自签 CA 与 agent token 探测 docker/file 两个入站端口。
type ProbeClient struct {
	// Timeout 是单个 HTTP 请求的超时时间，避免节点不可达时阻塞注册流程。
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
	// 探测必须使用注册时保存的 CA；不允许跳过 TLS 校验，否则无法发现节点证书配置错误。
	pool, err := BuildCertPool(caCertPEM)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout: c.Timeout,
		// 探测每个节点都临时构造 client，套用 IdleConnTimeout 收敛空闲连接，
		// 避免探测自身在节点配置各异时残留连接。
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    pool,
				MinVersion: tls.VersionTLS12,
			},
			IdleConnTimeout:     IdleConnTimeout,
			MaxIdleConnsPerHost: MaxIdleConnsPerHost,
		},
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
		// agent token 是节点注册后的通信凭据，docker 与 file 两个端点都使用 Bearer 头。
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 探测错误只保留短 body，避免 agent 返回大段 HTML/日志时污染节点状态说明。
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
