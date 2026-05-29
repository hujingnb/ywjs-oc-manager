// client.go — ocops 包的类型化 HTTP 客户端。
//
// Client 封装 net/http.Client，统一处理 Bearer 鉴权、JSON 序列化/反序列化
// 以及 HTTP 状态码 → 哨兵错误映射。上层调用方（client_cron.go / client_kanban.go
// 等）只需调用 DoJSON，无需关心 HTTP 层细节。
package ocops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Endpoint 是单个 app 的 oc-ops 访问坐标：基址 + per-app 控制 token。
// 真实寻址（k8s Service DNS）与 token 来源由 spec-A 注入，spec-E 经 service.OcOpsResolver 解耦。
type Endpoint struct {
	// BaseURL 是 oc-ops 服务根地址，例如 "http://app-slug.ocops.svc.cluster.local:8080"。
	BaseURL string
	// Token 是该 app 实例的 Bearer token（OC_OPS_TOKEN），由平台注入，不得共用。
	Token string
}

// Client 是 oc-ops 的类型化 HTTP 客户端。
// 通过 NewClient 构造，支持注入自定义 http.Client（便于测试与超时配置）。
type Client struct {
	// httpClient 是底层 HTTP 执行器；nil 时使用 http.DefaultClient。
	httpClient *http.Client
}

// NewClient 构造客户端；h 为 nil 时使用 http.DefaultClient。
func NewClient(h *http.Client) *Client {
	if h == nil {
		h = http.DefaultClient
	}
	return &Client{httpClient: h}
}

// DoJSON 发一次 JSON 请求并处理响应：
//   - reqBody 非 nil 时序列化为请求体，同时设置 Content-Type: application/json；
//   - 2xx 时若 out 非 nil 则将响应 body 解码到 out；
//   - 非 2xx 时用 statusToErr 映射哨兵错误，并将响应 body 中的 message 字段拼入错误文本。
//
// 调用方负责传入带超时的 ctx 以避免无限阻塞。
func (c *Client) DoJSON(ctx context.Context, ep Endpoint, method, path string, reqBody, out any) error {
	// 序列化请求体（如有）
	var body io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("ocops: marshal 请求体: %w", err)
		}
		body = bytes.NewReader(b)
	}

	// 构造带 context 的请求
	req, err := http.NewRequestWithContext(ctx, method, ep.BaseURL+path, body)
	if err != nil {
		return fmt.Errorf("ocops: 构造请求: %w", err)
	}

	// 统一注入 Bearer token 鉴权头
	req.Header.Set("Authorization", "Bearer "+ep.Token)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// 网络级错误归入 ErrCLI（与 502 语义一致：上游不可达）
		return fmt.Errorf("%w: %v", ErrCLI, err)
	}
	defer resp.Body.Close()

	// 非 2xx：映射哨兵错误并附上响应中的 message
	if sentinel := statusToErr(resp.StatusCode); sentinel != nil {
		// 尝试解析契约错误体 {"code":"...","message":"..."}，失败时 message 保持空字符串
		var e struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("%w: %s", sentinel, e.Message)
	}

	// 2xx：按需解码响应体
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("%w: 解码响应: %v", ErrOutputInvalid, err)
		}
	}
	return nil
}
