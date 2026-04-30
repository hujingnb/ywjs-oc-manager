package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// FileEntry 表示 agent 远端目录下的一个条目。
// IsDir / Size / Mode 仅作展示用途，路径合法性由 manager 服务层 SafePath 单独校验。
type FileEntry struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
	Mode  string `json:"mode"`
}

// FileListing 是 list 请求的标准化响应。
type FileListing struct {
	Path    string      `json:"path"`
	Entries []FileEntry `json:"entries"`
}

// AgentFileClient 通过 HTTP 调用 runtime agent 提供的文件 API。
// 所有方法都接收 ctx，超时与取消由调用方控制；
// agent token 通过 Authorization 头传递，为空时表示该 agent 未启用鉴权（仅用于本地调试场景）。
type AgentFileClient struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// NewFileClient 创建一个 agent 文件 client。
func NewFileClient(baseURL, token string) *AgentFileClient {
	return &AgentFileClient{BaseURL: baseURL, Token: token}
}

func (c *AgentFileClient) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

// List 列出指定路径下的条目。
func (c *AgentFileClient) List(ctx context.Context, remotePath string) (FileListing, error) {
	endpoint, err := c.endpoint("/v1/files/list")
	if err != nil {
		return FileListing{}, err
	}
	q := url.Values{}
	q.Set("path", remotePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return FileListing{}, err
	}
	c.authorize(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return FileListing{}, err
	}
	defer resp.Body.Close()
	if err := expectSuccess(resp, "list files"); err != nil {
		return FileListing{}, err
	}
	var listing FileListing
	if err := json.NewDecoder(resp.Body).Decode(&listing); err != nil {
		return FileListing{}, fmt.Errorf("解析 list 响应失败: %w", err)
	}
	return listing, nil
}

// Download 以流的形式下载指定文件。
// 调用方必须 Close 返回的 ReadCloser，否则会泄露 HTTP 连接。
func (c *AgentFileClient) Download(ctx context.Context, remotePath string) (io.ReadCloser, error) {
	endpoint, err := c.endpoint("/v1/files/get")
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("path", remotePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	c.authorize(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("download file failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	return resp.Body, nil
}

// Upload 将本地内容上传到 agent 指定路径。
// content 在调用结束前必须保持有效；nil 表示上传零字节文件。
func (c *AgentFileClient) Upload(ctx context.Context, remotePath string, content io.Reader) error {
	endpoint, err := c.endpoint("/v1/files/put")
	if err != nil {
		return err
	}
	q := url.Values{}
	q.Set("path", remotePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"?"+q.Encode(), content)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	c.authorize(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return expectSuccess(resp, "upload file")
}

// Archive 在节点上对指定目录打 tar.gz 包并返回流。
// 仅供工作目录下载、知识库归档等只读链路；写操作走 Upload 或 Delete。
func (c *AgentFileClient) Archive(ctx context.Context, remotePath string) (io.ReadCloser, error) {
	endpoint, err := c.endpoint("/v1/files/archive")
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("path", remotePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	c.authorize(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("archive failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	return resp.Body, nil
}

// Delete 删除 agent 上的文件或目录（递归）。
func (c *AgentFileClient) Delete(ctx context.Context, remotePath string) error {
	endpoint, err := c.endpoint("/v1/files/delete")
	if err != nil {
		return err
	}
	q := url.Values{}
	q.Set("path", remotePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"?"+q.Encode(), bytes.NewReader(nil))
	if err != nil {
		return err
	}
	c.authorize(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return expectSuccess(resp, "delete path")
}

func (c *AgentFileClient) endpoint(p string) (string, error) {
	if c.BaseURL == "" {
		return "", fmt.Errorf("agent file client 未配置 BaseURL")
	}
	endpoint, err := url.JoinPath(c.BaseURL, p)
	if err != nil {
		return "", err
	}
	return endpoint, nil
}

func (c *AgentFileClient) authorize(req *http.Request) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
}

func expectSuccess(resp *http.Response, op string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("%s failed: status=%d body=%s", op, resp.StatusCode, strings.TrimSpace(string(body)))
}

// ResolveRemotePath 是 service 层组合 base+组织+应用路径时的便利封装。
// 这里不做任何 SafePath 校验，仅做斜杠拼接，校验由调用方在写库前完成。
func ResolveRemotePath(base string, segments ...string) string {
	parts := append([]string{strings.TrimRight(base, "/")}, segments...)
	return path.Join(parts...)
}
