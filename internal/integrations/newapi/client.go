// Package newapi 封装与 new-api 网关的交互。
//
// 设计要点：
//   - 仅暴露 manager 当前实际使用的能力，避免泄漏 new-api 全部 API 表面；
//   - 错误统一映射成 sentinel error，便于 worker handler 区分“需重试 vs 立即失败”；
//   - 调用方必须传入超时 context，client 自身不内置长超时，避免阻塞 worker 线程。
package newapi

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

// 与 new-api 调用相关的错误。
var (
	ErrNotFound       = errors.New("new-api 资源不存在")
	ErrUnauthorized   = errors.New("new-api 鉴权失败")
	ErrConflict       = errors.New("new-api 资源冲突")
	ErrUpstream       = errors.New("new-api 网关异常")
	ErrPayloadInvalid = errors.New("new-api 返回体无法解析")
)

// APIKey 描述 new-api 中的 token 实体。
type APIKey struct {
	ID         int64    `json:"id"`
	UserID     int64    `json:"user_id"`
	Name       string   `json:"name"`
	Key        string   `json:"key,omitempty"`
	RemainQuota int64   `json:"remain_quota"`
	Models     []string `json:"models"`
	Status     int      `json:"status"`
}

// CreateAPIKeyInput 是创建 token 的入参。
type CreateAPIKeyInput struct {
	UserID     int64
	Name       string
	Models     []string
	Quota      int64
	Group      string
	UnlimitedQ bool
}

// Client 是 new-api 的 HTTP 客户端。
type Client struct {
	BaseURL    string
	AdminToken string
	HTTPClient *http.Client
}

// NewClient 构造 new-api client，未提供 HTTPClient 时使用 http.DefaultClient。
func NewClient(baseURL, adminToken string) *Client {
	return &Client{BaseURL: baseURL, AdminToken: adminToken}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

// CreateAPIKey 调用 new-api 创建 token。
// 错误按状态码统一映射；2xx + ok=false 也按 ErrUpstream 处理，避免静默成功。
func (c *Client) CreateAPIKey(ctx context.Context, input CreateAPIKeyInput) (APIKey, error) {
	if c.BaseURL == "" {
		return APIKey{}, fmt.Errorf("new-api client 未配置 BaseURL")
	}
	body := map[string]any{
		"user_id":              input.UserID,
		"name":                 input.Name,
		"models":               input.Models,
		"remain_quota":         input.Quota,
		"unlimited_quota":      input.UnlimitedQ,
		"group":                input.Group,
		"expired_time":         -1,
		"status":               1,
	}
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    APIKey `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/token/", body, &response); err != nil {
		return APIKey{}, err
	}
	if !response.Success {
		return APIKey{}, fmt.Errorf("%w: %s", ErrUpstream, response.Message)
	}
	return response.Data, nil
}

// GetAPIKey 查询 token 详情。
func (c *Client) GetAPIKey(ctx context.Context, id int64) (APIKey, error) {
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    APIKey `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/token/%d", id), nil, &response); err != nil {
		return APIKey{}, err
	}
	if !response.Success {
		return APIKey{}, fmt.Errorf("%w: %s", ErrUpstream, response.Message)
	}
	return response.Data, nil
}

// SetAPIKeyStatus 启用或禁用 token。
// status: 1 启用、2 禁用。
func (c *Client) SetAPIKeyStatus(ctx context.Context, id int64, status int) error {
	body := map[string]any{"id": id, "status": status}
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := c.do(ctx, http.MethodPut, "/api/token/?status_only=true", body, &response); err != nil {
		return err
	}
	if !response.Success {
		return fmt.Errorf("%w: %s", ErrUpstream, response.Message)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, body any, target any) error {
	endpoint, err := url.JoinPath(c.BaseURL, path)
	if err != nil {
		return err
	}
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("序列化请求失败: %w", err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bodyReader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.AdminToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AdminToken)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("调用 new-api 失败: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return ErrUnauthorized
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode == http.StatusConflict:
		return ErrConflict
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: status=%d", ErrUpstream, resp.StatusCode)
	}
	if target == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("%w: %s", ErrPayloadInvalid, err.Error())
	}
	return nil
}
