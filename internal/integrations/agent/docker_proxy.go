package agent

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
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

// NewDockerClientForNode 构造一个面向单个 runtime node 的 docker SDK client。
//
// 关键点：
//   - endpoint：agent 暴露的 docker 代理 URL（https://host:7001 或 https://ip:7001）；
//   - agentToken：注册成功后 manager 缓存的长期通信令牌，调用时通过 Authorization 头注入；
//   - caCertPEM：agent 自签 CA 证书 PEM，用于 manager 端 TLS 校验；
//   - 自定义 RoundTripper 在每个请求前注入 Bearer 头并把 path 重写为 /v1/docker/<rest>，
//     这样 docker SDK 内置的 host="docker"、path=/_ping 之类相对路径会落到代理前缀下。
//
// 任何参数缺失或 PEM 不可解析都返回错误，避免运行时才暴露配置问题。
func NewDockerClientForNode(endpoint, agentToken, caCertPEM string, opts ...client.Opt) (*client.Client, error) {
	if strings.TrimSpace(endpoint) == "" {
		return nil, fmt.Errorf("agent docker endpoint 为空")
	}
	pool, err := buildCertPool(caCertPEM)
	if err != nil {
		return nil, err
	}
	transport := &bearerProxyTransport{
		base: &http.Transport{
			TLSClientConfig:       &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: dockerClientTimeout,
		},
		token: agentToken,
	}
	httpClient := &http.Client{Transport: transport, Timeout: dockerClientTimeout}
	defaults := []client.Opt{
		client.WithHost(endpoint),
		client.WithHTTPClient(httpClient),
		client.WithAPIVersionNegotiation(),
	}
	return client.NewClientWithOpts(append(defaults, opts...)...)
}

// buildCertPool 把 caCertPEM 解析为 x509.CertPool。
// 调用方必须提供 PEM；空字符串视作未配置 TLS 校验，第一版直接拒绝以防误用。
func buildCertPool(caCertPEM string) (*x509.CertPool, error) {
	if strings.TrimSpace(caCertPEM) == "" {
		return nil, fmt.Errorf("agent CA cert PEM 为空")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(caCertPEM)) {
		return nil, fmt.Errorf("无法解析 agent CA cert PEM，请检查 runtime_nodes.agent_tls_ca_cert")
	}
	return pool, nil
}

// bearerProxyTransport 在 docker SDK 请求出栈前完成两件事：
//  1. 注入 Authorization: Bearer <agentToken>，让 agent 端的中间件放行；
//  2. 把 docker SDK 默认生成的 /_ping、/v1.41/containers/... 之类 path
//     统一改写为 /v1/docker/<rest>，与 agent 暴露的代理前缀对齐。
//
// 直接修改请求会污染调用方的副本，因此用 req.Clone 之后再写头与 path。
type bearerProxyTransport struct {
	base  http.RoundTripper
	token string
}

func (t *bearerProxyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	if t.token != "" {
		cloned.Header.Set("Authorization", "Bearer "+t.token)
	}
	cloned.URL.Path = ensureProxyPrefix(cloned.URL.Path)
	cloned.URL.RawPath = ""
	// docker SDK 默认会把 URL.Scheme 设为 "http"（它假设 TLS 通过自定义 dial 完成，
	// 传统 docker:tcp 模式如此）。我们走的是真正的 HTTPS 反向代理，
	// 必须强制 scheme = https，否则 stdlib http.Transport 不会做 TLS 握手。
	cloned.URL.Scheme = "https"
	return t.base.RoundTrip(cloned)
}

// ensureProxyPrefix 在 docker SDK 生成的 path 前加上 /v1/docker。
// 已经带前缀（例如 manager 测试代码手动构造）的请求保持原样。
func ensureProxyPrefix(p string) string {
	if strings.HasPrefix(p, DockerProxyPathPrefix+"/") || p == DockerProxyPathPrefix {
		return p
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return DockerProxyPathPrefix + p
}
