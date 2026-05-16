package imagesync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog" // todo del
	"net/http"
	"net/url"
)

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
	rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))                                  // todo del
	slog.Error("[hujingnb][7] client:LoadImage raw agent response", "body", string(rawBody)) // todo del
	var payload struct {
		Info struct {
			ID string `json:"id"`
		} `json:"info"`
	}
	// original: if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
	if err := json.Unmarshal(rawBody, &payload); err != nil { // todo del origin: if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
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
