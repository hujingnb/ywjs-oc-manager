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

	"oc-manager/internal/integrations/httplog"
	mlog "oc-manager/internal/log"
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

// CreateDatasetRequest 描述创建 RAGFlow dataset 所需的 manager 输入。
type CreateDatasetRequest struct {
	// Name 是 RAGFlow dataset 名称。
	Name string
	// ChunkMethod 是 RAGFlow parser/chunk method。
	ChunkMethod string
	// EmbeddingModel 是人类可读模型名或 RAGFlow 接口接受的内部模型标识。
	EmbeddingModel string
}

// Dataset 描述 RAGFlow dataset 的基础字段和当前 embedding 配置。
type Dataset struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	EmbeddingModelID  string `json:"embd_id"`
	TenantEmbeddingID string `json:"tenant_embd_id"`
	ParserID          string `json:"parser_id"`
	DocNum            int32  `json:"doc_num"`
	ChunkNum          int32  `json:"chunk_num"`
}

// EmbeddingModel 描述 RAGFlow 可用 embedding 模型；InternalID 仅后端提交 RAGFlow 时使用。
type EmbeddingModel struct {
	Name       string
	Label      string
	Provider   string
	InternalID string
	Available  bool
}

// UnmarshalJSON 兼容 RAGFlow dataset 列表公开字段和旧内部字段名。
func (d *Dataset) UnmarshalJSON(raw []byte) error {
	var value struct {
		ID                string             `json:"id"`
		Name              string             `json:"name"`
		EmbeddingModelID  string             `json:"embd_id"`
		EmbeddingModel    string             `json:"embedding_model"`
		TenantEmbeddingID ragflowLooseString `json:"tenant_embd_id"`
		ParserID          string             `json:"parser_id"`
		ChunkMethod       string             `json:"chunk_method"`
		DocNum            int32              `json:"doc_num"`
		DocumentCount     int32              `json:"document_count"`
		ChunkNum          int32              `json:"chunk_num"`
		ChunkCount        int32              `json:"chunk_count"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	d.ID = value.ID
	d.Name = value.Name
	d.EmbeddingModelID = value.EmbeddingModelID
	if d.EmbeddingModelID == "" {
		d.EmbeddingModelID = value.EmbeddingModel
	}
	d.TenantEmbeddingID = string(value.TenantEmbeddingID)
	d.ParserID = value.ParserID
	if d.ParserID == "" {
		d.ParserID = value.ChunkMethod
	}
	d.DocNum = value.DocNum
	if d.DocNum == 0 {
		d.DocNum = value.DocumentCount
	}
	d.ChunkNum = value.ChunkNum
	if d.ChunkNum == 0 {
		d.ChunkNum = value.ChunkCount
	}
	return nil
}

// ragflowLooseString 兼容 RAGFlow 部分字段在不同版本中 string/number 混用的响应。
// manager 只展示该值，不参与数值计算，因此统一转成字符串可避免单个兼容字段拖垮整个 dataset 解码。
type ragflowLooseString string

// UnmarshalJSON 把 JSON string 或 number 都收敛为字符串，null/空值保持零值。
func (s *ragflowLooseString) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		*s = ""
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		*s = ragflowLooseString(text)
		return nil
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return err
	}
	*s = ragflowLooseString(number.String())
	return nil
}

// Document 描述 RAGFlow document 的基础字段。
type Document struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location"`
	Size     int64  `json:"size"`
	Run      string `json:"run"`
	Type     string `json:"type"`
	// ProgressMsg 是 RAGFlow 文档解析的多行进度/日志，解析失败（run=FAIL）时其尾部含具体失败原因
	// （如 embedding 报错）。后台轮询用它填充本地 last_error，供前端在「解析失败」时展示真实原因。
	ProgressMsg string `json:"progress_msg"`
}

// uploadDocumentResponse 兼容 RAGFlow 上传接口的 data 数组响应。
// 客户端一次只上传一个 file 字段，因此取数组中的第一个 document 作为本次上传结果。
type uploadDocumentResponse struct {
	Document Document
}

// UnmarshalJSON 兼容旧测试桩中的单对象响应和官方文档中的数组响应。
func (r *uploadDocumentResponse) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return fmt.Errorf("RAGFlow 上传响应缺少 document")
	}
	if raw[0] == '[' {
		var docs []Document
		if err := json.Unmarshal(raw, &docs); err != nil {
			return err
		}
		if len(docs) == 0 {
			return fmt.Errorf("RAGFlow 上传响应 document 数组为空")
		}
		return r.setDocument(docs[0])
	}
	var doc Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	return r.setDocument(doc)
}

func (r *uploadDocumentResponse) setDocument(doc Document) error {
	if strings.TrimSpace(doc.ID) == "" {
		return fmt.Errorf("RAGFlow 上传响应缺少 document id")
	}
	r.Document = doc
	return nil
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

// UnmarshalJSON 同时兼容 manager 旧测试桩字段和 RAGFlow 官方 retrieval 字段。
func (c *RetrievalChunk) UnmarshalJSON(raw []byte) error {
	var value struct {
		ID              string  `json:"id"`
		Content         string  `json:"content"`
		DocumentID      string  `json:"document_id"`
		DocumentName    string  `json:"document_name"`
		DocumentKeyword string  `json:"document_keyword"`
		DatasetID       string  `json:"dataset_id"`
		KBID            string  `json:"kb_id"`
		Similarity      float64 `json:"similarity"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	c.ID = value.ID
	c.Content = value.Content
	c.DocumentID = value.DocumentID
	c.DocumentName = value.DocumentName
	if c.DocumentName == "" {
		c.DocumentName = value.DocumentKeyword
	}
	c.DatasetID = value.DatasetID
	if c.DatasetID == "" {
		c.DatasetID = value.KBID
	}
	c.Similarity = value.Similarity
	return nil
}

type retrievalDocAgg struct {
	DocID        string `json:"doc_id"`
	DocName      string `json:"doc_name"`
	DocumentID   string `json:"document_id"`
	DocumentName string `json:"document_name"`
}

func (a retrievalDocAgg) documentID() string {
	if a.DocID != "" {
		return a.DocID
	}
	return a.DocumentID
}

func (a retrievalDocAgg) documentName() string {
	if a.DocName != "" {
		return a.DocName
	}
	return a.DocumentName
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
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("ragflow baseURL 必须使用 http 或 https 协议")
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
		// 注入带日志的 transport：所有出站 RAGFlow 调用在传输层统一记录元数据，
		// 保留原超时设置不变，注入不改变请求语义。
		http: &http.Client{
			Timeout:   timeout,
			Transport: httplog.New(nil, mlog.LogTypeRAGFlow),
		},
	}, nil
}

// CreateDataset 创建 RAGFlow dataset。
func (c *Client) CreateDataset(ctx context.Context, req CreateDatasetRequest) (Dataset, error) {
	var out Dataset
	body := map[string]string{
		"name":         req.Name,
		"chunk_method": req.ChunkMethod,
	}
	if strings.TrimSpace(req.EmbeddingModel) != "" {
		body["embedding_model"] = strings.TrimSpace(req.EmbeddingModel)
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/datasets", nil, body, &out); err != nil {
		return Dataset{}, err
	}
	return out, nil
}

// GetDataset 实时读取 RAGFlow dataset 信息。
func (c *Client) GetDataset(ctx context.Context, datasetID string) (Dataset, error) {
	query := url.Values{}
	query.Set("id", datasetID)
	query.Set("page", "1")
	query.Set("page_size", "1")
	query.Set("include_parsing_status", "true")
	var out []Dataset
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/datasets", query, nil, &out); err != nil {
		return Dataset{}, err
	}
	if len(out) == 0 {
		return Dataset{}, fmt.Errorf("RAGFlow dataset %s 不存在", datasetID)
	}
	return out[0], nil
}

// UpdateDatasetEmbeddingModel 修改 RAGFlow dataset 的 embedding 模型。
func (c *Client) UpdateDatasetEmbeddingModel(ctx context.Context, datasetID, embeddingModel string) error {
	body := map[string]string{"embedding_model": strings.TrimSpace(embeddingModel)}
	return c.doJSON(ctx, http.MethodPut, c.apiPath("/api/v1/datasets", datasetID), nil, body, nil)
}

// RunDatasetEmbedding 触发指定 dataset 下全部文件重新 embedding。
func (c *Client) RunDatasetEmbedding(ctx context.Context, datasetID string) error {
	const pageSize int32 = 100
	var (
		documentIDs []string
		seenDocs    int32
	)
	for page := int32(1); ; page++ {
		docs, total, err := c.ListDocuments(ctx, datasetID, page, pageSize, "", "")
		if err != nil {
			return err
		}
		if len(docs) == 0 {
			break
		}
		seenDocs += int32(len(docs))
		for _, doc := range docs {
			if id := strings.TrimSpace(doc.ID); id != "" {
				documentIDs = append(documentIDs, id)
			}
		}
		if int32(len(docs)) < pageSize {
			break
		}
		if total > 0 && seenDocs >= total {
			break
		}
	}
	if len(documentIDs) == 0 {
		return nil
	}
	return c.ParseDocuments(ctx, datasetID, documentIDs)
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

	var out uploadDocumentResponse
	if err := c.do(req, &out); err != nil {
		return Document{}, err
	}
	return out.Document, nil
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
		Docs          []Document `json:"docs"`
		Items         []Document `json:"items"`
		Total         *int32     `json:"total"`
		TotalDatasets *int32     `json:"total_datasets"`
	}
	if err := c.doJSON(ctx, http.MethodGet, c.apiPath("/api/v1/datasets", datasetID, "documents"), query, nil, &out); err != nil {
		return nil, 0, err
	}
	items := out.Docs
	if len(items) == 0 {
		items = out.Items
	}
	total := int32(0)
	if out.Total != nil {
		total = *out.Total
	} else if out.TotalDatasets != nil {
		total = *out.TotalDatasets
	}
	return items, total, nil
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

// StopParsing 取消指定 dataset 下 running 文档的解析（DELETE /chunks）：RAGFlow 会 cancel 任务、
// 把 run 置为 stopped、清空已生成 chunk/索引。仅对 run=RUNNING 文档有效，非 running 会返回业务错误。
func (c *Client) StopParsing(ctx context.Context, datasetID string, documentIDs []string) error {
	body := map[string][]string{"document_ids": documentIDs}
	return c.doJSON(ctx, http.MethodDelete, c.apiPath("/api/v1/datasets", datasetID, "chunks"), nil, body, nil)
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
		Chunks  []RetrievalChunk  `json:"chunks"`
		DocAggs []retrievalDocAgg `json:"doc_aggs"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/retrieval", nil, body, &out); err != nil {
		return nil, err
	}
	applyDocAggNames(out.Chunks, out.DocAggs)
	return out.Chunks, nil
}

func applyDocAggNames(chunks []RetrievalChunk, docAggs []retrievalDocAgg) {
	if len(chunks) == 0 || len(docAggs) == 0 {
		return
	}
	names := make(map[string]string, len(docAggs))
	for _, agg := range docAggs {
		if id, name := agg.documentID(), agg.documentName(); id != "" && name != "" {
			names[id] = name
		}
	}
	for index := range chunks {
		if chunks[index].DocumentName != "" {
			continue
		}
		if name := names[chunks[index].DocumentID]; name != "" {
			chunks[index].DocumentName = name
		}
	}
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
		if envelope.Code != nil {
			return fmt.Errorf("RAGFlow 响应缺少 data")
		}
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
