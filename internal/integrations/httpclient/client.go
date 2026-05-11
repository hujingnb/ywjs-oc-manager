// Package httpclient 提供 BaseHTTPClient 共用 HTTP 调用能力。
// agent / newapi 等 integrations 子包通过组合此 client 复用 URL 拼接 /
// 鉴权头注入 / JSON 序列化 / 状态码到 sentinel error 的映射。
package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// 共用 sentinel error，调用方用 errors.Is 判断。
var (
	ErrNotFound       = errors.New("资源不存在")
	ErrUnauthorized   = errors.New("未授权或 token 失效")
	ErrConflict       = errors.New("资源冲突")
	ErrUpstream       = errors.New("上游服务异常")
	ErrPayloadInvalid = errors.New("请求体无效")
)

// BaseHTTPClient 共用 HTTP 调用基础类。调用方组合方式持有实例。
type BaseHTTPClient struct {
	BaseURL    string       // 基础 URL，如 "http://agent:7002"
	HTTPClient *http.Client // 自定义 transport / timeout；nil 走 http.DefaultClient
	AuthToken  string       // Bearer token；空则不注入 Authorization
}

// DoJSON 发送 JSON 请求，反序列化响应到 out（可为 nil 跳过反序列化）。
// query 拼接到 path；body 序列化为 JSON；状态码非 2xx 时按 sentinel error 映射。
func (c *BaseHTTPClient) DoJSON(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	u := c.buildURL(path, query)
	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("序列化请求体失败: %w", err)
		}
		reqBody = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
	if err != nil {
		return fmt.Errorf("构造请求失败: %w", err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AuthToken)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()
	if err := mapStatusToError(resp); err != nil {
		return err
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("反序列化响应失败: %w", err)
		}
	}
	return nil
}

// DoStream 发送请求并把响应 body 流式写入 dst（用于二进制下载）。
func (c *BaseHTTPClient) DoStream(ctx context.Context, method, path string, query url.Values, dst io.Writer) error {
	u := c.buildURL(path, query)
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return fmt.Errorf("构造请求失败: %w", err)
	}
	if c.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AuthToken)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()
	if err := mapStatusToError(resp); err != nil {
		return err
	}
	if _, err := io.Copy(dst, resp.Body); err != nil {
		return fmt.Errorf("拷贝响应流失败: %w", err)
	}
	return nil
}

func (c *BaseHTTPClient) buildURL(path string, query url.Values) string {
	// integrations 内部调用方传入的 path 已经是可信相对路径；query 统一用 url.Values 编码，
	// 避免每个 client 手写 query string 时遗漏转义。
	u := c.BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return u
}

func (c *BaseHTTPClient) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func mapStatusToError(resp *http.Response) error {
	// 这里不解析上游业务 JSON，只按 HTTP 状态归一化为 sentinel error；
	// 具体协议字段由 agent/newapi 等上层 client 自己解释。
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrUnauthorized
	case resp.StatusCode == http.StatusConflict:
		return ErrConflict
	case resp.StatusCode == http.StatusBadRequest:
		return ErrPayloadInvalid
	default:
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%w: status=%d body=%s", ErrUpstream, resp.StatusCode, string(body))
	}
}
