package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"oc-manager/internal/integrations/httpclient"
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
	base       *httpclient.BaseHTTPClient
}

// NewFileClient 创建一个 agent 文件 client。
func NewFileClient(baseURL, token string) *AgentFileClient {
	c := &AgentFileClient{BaseURL: baseURL, Token: token}
	c.rebuildBase()
	return c
}

// SetHTTPClient 设置文件 API 共用 HTTP client。
//
// manager 访问 agent TLS 端口时必须注入信任该节点自签 CA 的 client；这里同时更新
// 直接流式方法和 BaseHTTPClient 方法，避免不同文件 API 方法走不同 TLS 配置。
func (c *AgentFileClient) SetHTTPClient(client *http.Client) {
	c.HTTPClient = client
	c.rebuildBase()
}

func (c *AgentFileClient) rebuildBase() {
	c.base = &httpclient.BaseHTTPClient{
		BaseURL:    c.BaseURL,
		HTTPClient: c.HTTPClient,
		AuthToken:  c.Token,
	}
}

func (c *AgentFileClient) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

// List 列出指定路径下的条目。
func (c *AgentFileClient) List(ctx context.Context, remotePath string) (FileListing, error) {
	if c.BaseURL == "" {
		return FileListing{}, fmt.Errorf("agent file client 未配置 BaseURL")
	}
	q := url.Values{}
	q.Set("path", remotePath)
	var listing FileListing
	if err := c.base.DoJSON(ctx, http.MethodGet, "/v1/files/list", q, nil, &listing); err != nil {
		return FileListing{}, err
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
	if c.BaseURL == "" {
		return fmt.Errorf("agent file client 未配置 BaseURL")
	}
	q := url.Values{}
	q.Set("path", remotePath)
	return c.base.DoJSON(ctx, http.MethodPost, "/v1/files/delete", q, nil, nil)
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

// ============================================================================
// Sprint 1 新增：scope-aware 方法。直接对应 agent 端 /v1/scopes/* 端点。
// service / worker 层用领域语义调（InitAppDirs / SyncAppKnowledge / ...），
// 不需要再手工拼路径。每个方法仅校验 baseURL 与 status，不在客户端做业务校验。
// ============================================================================

// InitAppDirs 让 agent 在节点上准备好 apps/{appID}/{knowledge,workspace,state,logs} 4 个目录。
// 操作幂等。容器创建前必须调一次。
func (c *AgentFileClient) InitAppDirs(ctx context.Context, appID string) error {
	return c.doScopePost(ctx, fmt.Sprintf("/v1/scopes/apps/%s/init", url.PathEscape(appID)), nil, "")
}

// SyncOrgKnowledge 把 manager 主副本里的组织级知识库 tar 流推到 agent，
// agent 解压后原子替换本地 orgs/{orgID}/knowledge/。
func (c *AgentFileClient) SyncOrgKnowledge(ctx context.Context, orgID string, tarStream io.Reader) error {
	return c.doScopePost(ctx,
		fmt.Sprintf("/v1/scopes/orgs/%s/knowledge/sync", url.PathEscape(orgID)),
		tarStream, "application/x-tar")
}

// SyncAppKnowledge 同 SyncOrgKnowledge，但走应用级。
func (c *AgentFileClient) SyncAppKnowledge(ctx context.Context, appID string, tarStream io.Reader) error {
	return c.doScopePost(ctx,
		fmt.Sprintf("/v1/scopes/apps/%s/knowledge/sync", url.PathEscape(appID)),
		tarStream, "application/x-tar")
}

// UploadOrgKnowledgeFile 单文件上传到 orgs/{orgID}/knowledge/{relPath}。
// relPath 必须为相对路径，agent 端会做沙箱校验。
func (c *AgentFileClient) UploadOrgKnowledgeFile(ctx context.Context, orgID, relPath string, content io.Reader) error {
	return c.doKnowledgeFile(ctx, http.MethodPut, "orgs", orgID, relPath, content)
}

// UploadAppRuntimeFile 把 manager 渲染的 Hermes runtime 配置文件
// (SOUL.md / config.yaml / .env / skills/<name>/SKILL.md)上传到节点
// apps/{appID}/.hermes/{relPath};agent 端写入节点本地文件系统,
// 容器启动时该目录整体 bind mount 到 /opt/data。
func (c *AgentFileClient) UploadAppRuntimeFile(ctx context.Context, appID, relPath string, content io.Reader) error {
	endpoint, err := c.endpoint(fmt.Sprintf("/v1/scopes/apps/%s/runtime/file", url.PathEscape(appID)))
	if err != nil {
		return err
	}
	q := url.Values{}
	q.Set("path", relPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint+"?"+q.Encode(), content)
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
	return expectSuccess(resp, "upload app runtime file")
}

// UploadAppKnowledgeFile 同 UploadOrgKnowledgeFile，但走应用级。
func (c *AgentFileClient) UploadAppKnowledgeFile(ctx context.Context, appID, relPath string, content io.Reader) error {
	return c.doKnowledgeFile(ctx, http.MethodPut, "apps", appID, relPath, content)
}

// DeleteOrgKnowledge 删除组织级知识库的单文件或子目录。不存在视为成功（幂等）。
func (c *AgentFileClient) DeleteOrgKnowledge(ctx context.Context, orgID, relPath string) error {
	return c.doKnowledgeFile(ctx, http.MethodDelete, "orgs", orgID, relPath, nil)
}

// DeleteAppKnowledge 同 DeleteOrgKnowledge，但走应用级。
func (c *AgentFileClient) DeleteAppKnowledge(ctx context.Context, appID, relPath string) error {
	return c.doKnowledgeFile(ctx, http.MethodDelete, "apps", appID, relPath, nil)
}

// ListWorkspace 列举应用 workspace 下的内容。relPath 为根目录时传空串。
func (c *AgentFileClient) ListWorkspace(ctx context.Context, appID, relPath string) (WorkspaceListing, error) {
	if c.BaseURL == "" {
		return WorkspaceListing{}, fmt.Errorf("agent file client 未配置 BaseURL")
	}
	q := url.Values{}
	if relPath != "" {
		q.Set("path", relPath)
	}
	var listing WorkspaceListing
	if err := c.base.DoJSON(ctx, http.MethodGet,
		fmt.Sprintf("/v1/scopes/apps/%s/workspace", url.PathEscape(appID)),
		q, nil, &listing); err != nil {
		return WorkspaceListing{}, err
	}
	return listing, nil
}

// DownloadWorkspaceFile 流式下载应用 workspace 下的单文件。
// 调用方必须 Close 返回的 ReadCloser，否则会泄露 HTTP 连接。
func (c *AgentFileClient) DownloadWorkspaceFile(ctx context.Context, appID, relPath string) (io.ReadCloser, error) {
	if relPath == "" {
		return nil, fmt.Errorf("relPath 不能为空")
	}
	endpoint, err := c.endpoint(fmt.Sprintf("/v1/scopes/apps/%s/workspace/download", url.PathEscape(appID)))
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("path", relPath)
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
		return nil, fmt.Errorf("download workspace file failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	return resp.Body, nil
}

// StreamWorkspaceArchive 把应用 workspace 下的指定子目录流式打成 zip 写到 w。
// relPath 为空表示打整个 workspace。
func (c *AgentFileClient) StreamWorkspaceArchive(ctx context.Context, appID, relPath string, w io.Writer) error {
	if c.BaseURL == "" {
		return fmt.Errorf("agent file client 未配置 BaseURL")
	}
	q := url.Values{}
	if relPath != "" {
		q.Set("path", relPath)
	}
	return c.base.DoStream(ctx, http.MethodGet,
		fmt.Sprintf("/v1/scopes/apps/%s/workspace/archive", url.PathEscape(appID)),
		q, w)
}

// ArchiveApp 让 agent 把节点上 apps/{appID}/ 整目录归档到 archived/{appID}-{ts}/。
// 应用目录不存在视为成功（幂等）。
func (c *AgentFileClient) ArchiveApp(ctx context.Context, appID string) error {
	return c.doScopePost(ctx, fmt.Sprintf("/v1/scopes/apps/%s/archive", url.PathEscape(appID)), nil, "")
}

// CleanupArchive 触发 agent 清理 archived/ 下 mtime 超过 retentionDays 的归档目录。
// retentionDays 必须正整数。
func (c *AgentFileClient) CleanupArchive(ctx context.Context, retentionDays int) error {
	if retentionDays <= 0 {
		return fmt.Errorf("retentionDays 必须为正整数")
	}
	if c.BaseURL == "" {
		return fmt.Errorf("agent file client 未配置 BaseURL")
	}
	q := url.Values{}
	q.Set("retention_days", fmt.Sprintf("%d", retentionDays))
	return c.base.DoJSON(ctx, http.MethodPost, "/v1/scopes/cleanup-archives", q, nil, nil)
}

// WorkspaceListing 是 ListWorkspace 的标准化响应（与 agent /v1/scopes/.../workspace 输出一致）。
type WorkspaceListing struct {
	Path    string           `json:"path"`
	Entries []WorkspaceEntry `json:"entries"`
}

// WorkspaceEntry 描述 workspace 下的一个 entry。
type WorkspaceEntry struct {
	Name       string `json:"name"`
	Type       string `json:"type"` // file | dir
	Size       int64  `json:"size,omitempty"`
	ModifiedAt string `json:"modified_at"`
}

// doScopePost 是仅 POST + 可选 body 的 scope 端点统一入口。
// 用于无返回体或仅 status 校验的端点（init / sync / archive / cleanup 等）。
func (c *AgentFileClient) doScopePost(ctx context.Context, path string, body io.Reader, contentType string) error {
	endpoint, err := c.endpoint(path)
	if err != nil {
		return err
	}
	if body == nil {
		body = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.authorize(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return expectSuccess(resp, "scope post "+path)
}

// doKnowledgeFile 把 PUT/DELETE 单文件请求统一封装。
func (c *AgentFileClient) doKnowledgeFile(ctx context.Context, method, scopeType, scopeID, relPath string, content io.Reader) error {
	if relPath == "" {
		return fmt.Errorf("relPath 不能为空")
	}
	endpoint, err := c.endpoint(fmt.Sprintf("/v1/scopes/%s/%s/knowledge/file", scopeType, url.PathEscape(scopeID)))
	if err != nil {
		return err
	}
	q := url.Values{}
	q.Set("path", relPath)
	if content == nil {
		content = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint+"?"+q.Encode(), content)
	if err != nil {
		return err
	}
	if method == http.MethodPut {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	c.authorize(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return expectSuccess(resp, fmt.Sprintf("%s knowledge file", strings.ToLower(method)))
}
