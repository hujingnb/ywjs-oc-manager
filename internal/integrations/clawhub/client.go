package clawhub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// ClawHubClient 是 ClawHub 公开 API 的只读客户端（无鉴权）。
// 直接持有 http.Client，不依赖项目内部其他包，保持纯标准库依赖。
type ClawHubClient struct {
	baseURL string
	http    *http.Client
}

// NewClient 构造客户端；timeout<=0 时用 10s 默认，避免无限阻塞。
func NewClient(baseURL string, timeout time.Duration) *ClawHubClient {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &ClawHubClient{baseURL: baseURL, http: &http.Client{Timeout: timeout}}
}

// getJSON 发 GET 请求并把 JSON 响应解码进 out。
// 非 2xx 状态码视为错误，调用方无需再检查 HTTP 状态。
func (c *ClawHubClient) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	endpoint := c.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("构造 ClawHub 请求失败: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("请求 ClawHub 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ClawHub 返回非 2xx 状态: %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("解析 ClawHub 响应失败: %w", err)
	}
	return nil
}

// Search 搜索 skill（q 为空时列出全部）。cursor 为分页游标。
// clawhubcn 用同一个 /api/v1/skills 端点：无 q 列出全部，带 q 做关键词过滤；
// 没有独立的 /api/v1/search 路由（实测请求该路径会被服务端直接 reset，HTTP 000）。
func (c *ClawHubClient) Search(ctx context.Context, q, cursor string) (SearchResult, error) {
	query := url.Values{}
	if q != "" {
		query.Set("q", q)
	}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	var out SearchResult
	if err := c.getJSON(ctx, "/api/v1/skills", query, &out); err != nil {
		return SearchResult{}, err
	}
	return out, nil
}

// GetSkill 取单个 skill 的详细元数据。
// clawhubcn 详情响应为 {"skill":{...},"latestVersion":{"version":...}}，需解包 skill 字段；
// skill 内缺版本时用顶层 latestVersion 补齐。
func (c *ClawHubClient) GetSkill(ctx context.Context, slug string) (Skill, error) {
	var out struct {
		Skill         Skill `json:"skill"`
		LatestVersion struct {
			Version string `json:"version"`
		} `json:"latestVersion"`
	}
	if err := c.getJSON(ctx, "/api/v1/skills/"+url.PathEscape(slug), nil, &out); err != nil {
		return Skill{}, err
	}
	sk := out.Skill
	if sk.Version == "" {
		sk.Version = out.LatestVersion.Version
	}
	return sk, nil
}

// ListVersions 列出某 skill 的全部历史版本。
// clawhubcn 响应为 {"items":[{"version":...}],"nextCursor":...}，需解包 items。
func (c *ClawHubClient) ListVersions(ctx context.Context, slug string) ([]SkillVersion, error) {
	var out struct {
		Items []SkillVersion `json:"items"`
	}
	if err := c.getJSON(ctx, "/api/v1/skills/"+url.PathEscape(slug)+"/versions", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// Download 下载指定版本的 skill 归档原始字节（ClawHub 返回 zip 格式）。
// 直接读取响应体，不经过 JSON 解码，保留二进制完整性。
func (c *ClawHubClient) Download(ctx context.Context, slug, version string) ([]byte, error) {
	query := url.Values{"slug": {slug}, "version": {version}}
	endpoint := c.baseURL + "/api/v1/download?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("构造 ClawHub 下载请求失败: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ClawHub 下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ClawHub 下载返回非 2xx: %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
