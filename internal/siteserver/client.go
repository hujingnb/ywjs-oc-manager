package siteserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// 编译期断言：HTTPSiteListClient 必须实现 SiteListClient 接口。
var _ SiteListClient = (*HTTPSiteListClient)(nil)

// HTTPSiteListClient 调 manager 内部端点拉活跃站点列表（契约见 Plan 3 / Plan 4）。
type HTTPSiteListClient struct {
	url   string
	token string
	http  *http.Client
}

// NewHTTPSiteListClient 构造客户端，带 10s 超时（同步是后台任务，不必长等）。
func NewHTTPSiteListClient(url, token string) *HTTPSiteListClient {
	return &HTTPSiteListClient{url: url, token: token, http: &http.Client{Timeout: 10 * time.Second}}
}

// listResponse 对应 manager 端点 JSON：{"sites":[...]}。
type listResponse struct {
	Sites []SiteRecord `json:"sites"`
}

// ListActiveSites 调 manager 端点并解析返回；非 200 视为失败（syncer 据此保留旧快照）。
func (c *HTTPSiteListClient) ListActiveSites(ctx context.Context) ([]SiteRecord, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-OC-Site-Sync-Token", c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("siteserver: 调 manager 同步端点失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("siteserver: manager 同步端点返回 %d", resp.StatusCode)
	}
	var out listResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("siteserver: 解析同步响应失败: %w", err)
	}
	return out.Sites, nil
}
