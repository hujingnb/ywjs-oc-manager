// Package agent 封装 manager 访问 runtime-agent 的 HTTP/TLS 客户端能力。
// docker_proxy 负责把 agent 暴露的 Docker 代理端口转换成 Docker SDK 可用的 client。
package agent

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/docker/docker/client"
)

// DockerProxyPathPrefix 是 manager 调用 agent docker 代理时统一附加的路径前缀。
// agent 端的 ReverseProxy 会把它裁掉再转给 docker socket，详见 runtime/agent/proxy.go。
const DockerProxyPathPrefix = "/v1/docker"

// dockerClientTimeout 控制每个 docker SDK 请求的硬上限。
// 大型 ContainerWait/Logs 等流式 API 在调用方自行用 ctx 覆盖；
// 这里设置 30s 避免 API 调用 hang 死整个 worker。
const dockerClientTimeout = 30 * time.Second

// IdleConnTimeout 限制空闲 keep-alive 连接的存活时长，是连接泄漏的兜底。
// 关键：*http.Transport 默认 IdleConnTimeout=0（空闲连接永不回收），一旦某个 client 被遗弃
// 又没 Close，其空闲连接会无限堆积、占满本机临时端口（出现
// "connect: cannot assign requested address"）。设为 90s 后，即使 client 被遗弃，其空闲连接
// 也会自行关闭、释放端口，对应的 transport 随之能被 GC。manager 访问 agent 的所有 transport
// 都应套用此值。
const IdleConnTimeout = 90 * time.Second

// MaxIdleConnsPerHost 限制每个目标 host 的空闲连接上限，进一步约束复用场景下的连接占用。
// 默认值仅 2；agent 端口同时承载多种并发操作，放宽到 4 兼顾复用与连接数收敛。
const MaxIdleConnsPerHost = 4

// NewDockerClientForNode 构造一个面向单个 runtime node 的 docker SDK client。
//
// 关键点：
//   - endpoint：agent 暴露的 docker 代理 URL（https://host:7001 或 https://ip:7001）；
//     函数内部会把它改写成 tcp://host:7001/v1/docker 喂给 docker SDK，让 SDK 自动处理：
//     1. basePath = /v1/docker，所有 REST 请求与 hijack 请求的 path 都自动加前缀；
//     2. proto = "tcp"，使 hijack dialer 走 tls.Dial("tcp", addr, tlsConfig) 拨真 TLS 连接，
//     否则 SDK 会用 net.Dial("https", ...) 失败（"unknown network https"）；
//     3. scheme = "https"（由 SDK 根据 tlsConfig != nil 推导），保证非 hijack REST 走 TLS。
//   - agentToken：注册成功后 manager 缓存的长期通信令牌，通过 client.WithHTTPHeaders
//     注入为默认 Authorization 头；这样 REST 与 hijack（exec attach 等）都会自动携带。
//   - caCertPEM：agent 自签 CA 证书 PEM，用于 manager 端 TLS 校验。
//
// 任何参数缺失或 PEM 不可解析都返回错误，避免运行时才暴露配置问题。
func NewDockerClientForNode(endpoint, agentToken, caCertPEM string, opts ...client.Opt) (*client.Client, error) {
	if strings.TrimSpace(endpoint) == "" {
		return nil, fmt.Errorf("agent docker endpoint 为空")
	}
	pool, err := BuildCertPool(caCertPEM)
	if err != nil {
		return nil, err
	}
	parsedURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("解析 agent endpoint 失败: %w", err)
	}
	if parsedURL.Host == "" {
		return nil, fmt.Errorf("agent endpoint 缺少 host: %q", endpoint)
	}

	tlsConfig := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	// 直接使用 *http.Transport 让 docker SDK 能把它识别为 baseTransport（用于关闭空闲连接等），
	// 同时 SDK 才能在带 https URL 的请求上走 TLS。
	transport := &http.Transport{
		TLSClientConfig:       tlsConfig,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: dockerClientTimeout,
		IdleConnTimeout:       IdleConnTimeout,
		MaxIdleConnsPerHost:   MaxIdleConnsPerHost,
	}
	httpClient := &http.Client{Transport: transport, Timeout: dockerClientTimeout}

	// 关键：把 endpoint 转换成 tcp://host:port/v1/docker 形式喂给 docker SDK。
	// 详见函数 doc 注释。
	sdkHost := "tcp://" + parsedURL.Host + DockerProxyPathPrefix

	defaults := []client.Opt{
		client.WithHost(sdkHost),
		client.WithHTTPClient(httpClient),
		client.WithHTTPHeaders(map[string]string{
			"Authorization": "Bearer " + agentToken,
		}),
		client.WithAPIVersionNegotiation(),
	}
	return client.NewClientWithOpts(append(defaults, opts...)...)
}

// NewStreamingDockerClientForNode 与 NewDockerClientForNode 同义,
// 但返回的 http.Client 没有任何 timeout,专门用于长连接 ExecAttach 场景。
//
// 背景:NewDockerClientForNode 给 http.Client 设了 Timeout=30s(防普通 REST 调用 hang 死 worker),
// 但同一个 http.Client 也被 docker SDK 拿去做 ExecAttach 的 hijack,30s 后底层连接被强制关闭,
// 导致 docker stream EOF。这对短命的 health-check exec 没问题,
// 但对微信扫码 polling(可达数分钟)会直接断流,manager 端读到空 stdout 后 JSON 解析失败。
//
// 调用方:目前仅 channel.NewDockerExecutor(微信扫码长连接)。
// 其他 docker REST 调用继续走 NewDockerClientForNode 拿到带 timeout 的 client,防止 worker hang。
func NewStreamingDockerClientForNode(endpoint, agentToken, caCertPEM string, opts ...client.Opt) (*client.Client, error) {
	if strings.TrimSpace(endpoint) == "" {
		return nil, fmt.Errorf("agent docker endpoint 为空")
	}
	pool, err := BuildCertPool(caCertPEM)
	if err != nil {
		return nil, err
	}
	parsedURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("解析 agent endpoint 失败: %w", err)
	}
	if parsedURL.Host == "" {
		return nil, fmt.Errorf("agent endpoint 缺少 host: %q", endpoint)
	}

	tlsConfig := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	// streaming 场景仅保留 TLSHandshakeTimeout(防握手卡死),其余 timeout 全部禁用:
	//   - ResponseHeaderTimeout=0:ExecAttach 的响应头几乎即刻返回,无需限制
	//   - http.Client.Timeout=0:整个请求(含 hijack 后的长连接)不限时长
	transport := &http.Transport{
		TLSClientConfig:     tlsConfig,
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     IdleConnTimeout,
		MaxIdleConnsPerHost: MaxIdleConnsPerHost,
	}
	httpClient := &http.Client{Transport: transport}

	sdkHost := "tcp://" + parsedURL.Host + DockerProxyPathPrefix

	defaults := []client.Opt{
		client.WithHost(sdkHost),
		client.WithHTTPClient(httpClient),
		client.WithHTTPHeaders(map[string]string{
			"Authorization": "Bearer " + agentToken,
		}),
		client.WithAPIVersionNegotiation(),
	}
	return client.NewClientWithOpts(append(defaults, opts...)...)
}

// BuildCertPool 把 caCertPEM 解析为 x509.CertPool。
// 调用方必须提供 PEM；空字符串视作未配置 TLS 校验，第一版直接拒绝以防误用。
func BuildCertPool(caCertPEM string) (*x509.CertPool, error) {
	if strings.TrimSpace(caCertPEM) == "" {
		return nil, fmt.Errorf("agent CA cert PEM 为空")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(caCertPEM)) {
		return nil, fmt.Errorf("无法解析 agent CA cert PEM，请检查 runtime_nodes.agent_tls_ca_cert")
	}
	return pool, nil
}
