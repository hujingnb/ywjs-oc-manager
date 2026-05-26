// Package ragflow 封装 manager 后端访问 RAGFlow HTTP API 的最小能力集。
//
// 该包只处理 RAGFlow 协议、鉴权和响应错误；组织 / 实例权限、可写目标和状态归一化
// 都由 service 层负责，避免把 RAGFlow 自身可见性字段误用为 oc-manager 安全边界。
package ragflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

// Client 是 RAGFlow HTTP API 客户端。
type Client struct {
	// baseURL 保存已去掉尾部斜杠的 RAGFlow 服务地址。
	baseURL string
	// apiKey 是 manager 后端专用 RAGFlow API key，不下发给 Hermes。
	apiKey string
	// http 是带超时的底层 HTTP client。
	http *http.Client
}

// Dataset 描述 RAGFlow dataset 的基础字段。
type Dataset struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Document 描述 RAGFlow document 的基础字段。
type Document struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location"`
	Size     int64  `json:"size"`
	Run      string `json:"run"`
	Type     string `json:"type"`
}

// RetrievalChunk 描述 RAGFlow retrieval 返回的单个命中文本块。
type RetrievalChunk struct {
	ID           string  `json:"id"`
	Content      string  `json:"content"`
	DocumentID   string  `json:"document_id"`
	DocumentName string  `json:"document_name"`
	DatasetID    string  `json:"dataset_id"`
	Similarity   float64 `json:"similarity"`
}

// NewClient 构造 RAGFlow 客户端。
// timeout 为 0 时使用 30 秒，避免配置遗漏时出现无上限阻塞。
func NewClient(baseURL, apiKey string, timeout time.Duration) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	apiKey = strings.TrimSpace(apiKey)
	if baseURL == "" {
		return nil, fmt.Errorf("ragflow baseURL 不能为空")
	}
	parsed, err := url.ParseRequestURI(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("ragflow baseURL 非法")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("ragflow apiKey 不能为空")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: timeout},
	}, nil
}

// CreateDataset 创建 RAGFlow dataset。
func (c *Client) CreateDataset(ctx context.Context, name, chunkMethod string) (Dataset, error) {
	var out Dataset
	body := map[string]string{
		"name":         name,
		"chunk_method": chunkMethod,
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/datasets", nil, body, &out); err != nil {
		return Dataset{}, err
	}
	return out, nil
}

// DeleteDatasets 删除一个或多个 RAGFlow dataset。
func (c *Client) DeleteDatasets(ctx context.Context, ids []string) error {
	body := map[string][]string{"ids": ids}
	return c.doJSON(ctx, http.MethodDelete, "/api/v1/datasets", nil, body, nil)
}

// UploadDocument 上传单个文件到指定 dataset。
func (c *Client) UploadDocument(ctx context.Context, datasetID, filename string, body io.Reader) (Document, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return Document{}, fmt.Errorf("构造 RAGFlow 上传字段失败: %w", err)
	}
	if _, err := io.Copy(part, body); err != nil {
		return Document{}, fmt.Errorf("读取上传文件失败: %w", err)
	}
	if err := writer.Close(); err != nil {
		return Document{}, fmt.Errorf("结束 multipart body 失败: %w", err)
	}

	req, err := c.newRequest(ctx, http.MethodPost, c.apiPath("/api/v1/datasets", datasetID, "documents"), &buf)
	if err != nil {
		return Document{}, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")

	var out Document
	if err := c.do(req, &out); err != nil {
		return Document{}, err
	}
	return out, nil
}

// DownloadDocument 下载指定 document 的原始文件流。
func (c *Client) DownloadDocument(ctx context.Context, datasetID, documentID string) (io.ReadCloser, int64, error) {
	req, err := c.newRequest(ctx, http.MethodGet, c.apiPath("/api/v1/datasets", datasetID, "documents", documentID), nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("发送 RAGFlow 请求失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, 0, decodeErrorResponse(resp)
	}
	return resp.Body, resp.ContentLength, nil
}

// ListDocuments 按 dataset 分页列出 document。
func (c *Client) ListDocuments(ctx context.Context, datasetID string, page, pageSize int32, keywords, run string) ([]Document, int32, error) {
	query := url.Values{}
	if page > 0 {
		query.Set("page", strconv.FormatInt(int64(page), 10))
	}
	if pageSize > 0 {
		query.Set("page_size", strconv.FormatInt(int64(pageSize), 10))
	}
	if strings.TrimSpace(keywords) != "" {
		query.Set("keywords", strings.TrimSpace(keywords))
	}
	if strings.TrimSpace(run) != "" {
		query.Set("run", strings.TrimSpace(run))
	}
	var out struct {
		Docs  []Document `json:"docs"`
		Items []Document `json:"items"`
		Total int32      `json:"total"`
	}
	if err := c.doJSON(ctx, http.MethodGet, c.apiPath("/api/v1/datasets", datasetID, "documents"), query, nil, &out); err != nil {
		return nil, 0, err
	}
	items := out.Docs
	if len(items) == 0 {
		items = out.Items
	}
	return items, out.Total, nil
}

// DeleteDocuments 删除指定 dataset 下的一组 document。
func (c *Client) DeleteDocuments(ctx context.Context, datasetID string, documentIDs []string) error {
	body := map[string][]string{"ids": documentIDs}
	return c.doJSON(ctx, http.MethodDelete, c.apiPath("/api/v1/datasets", datasetID, "documents"), nil, body, nil)
}

// ParseDocuments 触发指定 document 解析。
func (c *Client) ParseDocuments(ctx context.Context, datasetID string, documentIDs []string) error {
	body := map[string][]string{"document_ids": documentIDs}
	return c.doJSON(ctx, http.MethodPost, c.apiPath("/api/v1/datasets", datasetID, "chunks"), nil, body, nil)
}

// Retrieve 对指定 dataset 集合执行 retrieval。
func (c *Client) Retrieve(ctx context.Context, datasetIDs []string, question string, topK int32) ([]RetrievalChunk, error) {
	body := map[string]any{
		"dataset_ids": datasetIDs,
		"question":    question,
	}
	if topK > 0 {
		body["top_k"] = topK
	}
	var out struct {
		Chunks []RetrievalChunk `json:"chunks"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/retrieval", nil, body, &out); err != nil {
		return nil, err
	}
	return out.Chunks, nil
}

func (c *Client) doJSON(ctx context.Context, method, pathValue string, query url.Values, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("序列化 RAGFlow 请求失败: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	if len(query) > 0 {
		pathValue += "?" + query.Encode()
	}
	req, err := c.newRequest(ctx, method, pathValue, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	return c.do(req, out)
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("发送 RAGFlow 请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeErrorResponse(resp)
	}
	if out == nil {
		return decodeOptionalCode(resp.Body)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取 RAGFlow 响应失败: %w", err)
	}
	return decodeJSONBody(raw, out)
}

func (c *Client) newRequest(ctx context.Context, method, pathValue string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+pathValue, body)
	if err != nil {
		return nil, fmt.Errorf("构造 RAGFlow 请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	return req, nil
}

func (c *Client) apiPath(parts ...string) string {
	escaped := make([]string, 0, len(parts))
	for index, part := range parts {
		if index == 0 {
			escaped = append(escaped, strings.TrimRight(part, "/"))
			continue
		}
		escaped = append(escaped, url.PathEscape(part))
	}
	return path.Join(escaped...)
}

func decodeErrorResponse(resp *http.Response) error {
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("RAGFlow HTTP %d 且读取错误响应失败: %w", resp.StatusCode, err)
	}
	if message := extractRAGFlowMessage(raw); message != "" {
		return fmt.Errorf("RAGFlow HTTP %d: %s", resp.StatusCode, message)
	}
	return fmt.Errorf("RAGFlow HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
}

func decodeOptionalCode(body io.Reader) error {
	raw, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("读取 RAGFlow 响应失败: %w", err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return decodeJSONBody(raw, nil)
}

func decodeJSONBody(raw []byte, out any) error {
	var envelope struct {
		Code    any             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("反序列化 RAGFlow 响应失败: %w", err)
	}
	if !isZeroCode(envelope.Code) {
		if envelope.Message != "" {
			return fmt.Errorf("RAGFlow 业务错误: %s", envelope.Message)
		}
		return fmt.Errorf("RAGFlow 业务错误: code=%v", envelope.Code)
	}
	if out == nil {
		return nil
	}
	target := bytes.TrimSpace(envelope.Data)
	if len(target) == 0 || bytes.Equal(target, []byte("null")) {
		target = raw
	}
	if err := json.Unmarshal(target, out); err != nil {
		return fmt.Errorf("反序列化 RAGFlow data 失败: %w", err)
	}
	return nil
}

func extractRAGFlowMessage(raw []byte) string {
	var envelope struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ""
	}
	return strings.TrimSpace(envelope.Message)
}

func isZeroCode(code any) bool {
	switch value := code.(type) {
	case nil:
		return true
	case float64:
		return value == 0
	case string:
		return value == "" || value == "0"
	default:
		return false
	}
}
