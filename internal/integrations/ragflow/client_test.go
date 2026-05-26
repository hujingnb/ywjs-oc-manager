package ragflow

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClientCreateDataset 验证创建 dataset 时只传 RAGFlow 生命周期字段，不携带 oc-manager 权限语义。
func TestClientCreateDataset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/datasets", r.URL.Path)
		assert.Equal(t, "Bearer secret", r.Header.Get("Authorization"))

		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "oc-org-1", body["name"])
		assert.Equal(t, "naive", body["chunk_method"])
		assert.NotContains(t, body, "permission")
		assert.NotContains(t, body, "tenant")

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"id":"ds-1","name":"oc-org-1"}}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server.URL)
	got, err := client.CreateDataset(context.Background(), "oc-org-1", "naive")
	require.NoError(t, err)
	assert.Equal(t, Dataset{ID: "ds-1", Name: "oc-org-1"}, got)
}

// TestClientUploadDocumentUsesMultipart 验证上传文档使用 RAGFlow 要求的 multipart file 字段并解析 document 元数据。
func TestClientUploadDocumentUsesMultipart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/datasets/ds-1/documents", r.URL.Path)
		assert.True(t, strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data"))

		reader, err := r.MultipartReader()
		require.NoError(t, err)
		part, err := reader.NextPart()
		require.NoError(t, err)
		assert.Equal(t, "file", part.FormName())
		assert.Equal(t, "report.md", part.FileName())
		content, err := io.ReadAll(part)
		require.NoError(t, err)
		assert.Equal(t, "# report", string(content))
		_, err = reader.NextPart()
		assert.ErrorIs(t, err, io.EOF)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"id":"doc-1","name":"report.md","size":12,"run":"UNSTART"}}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server.URL)
	got, err := client.UploadDocument(context.Background(), "ds-1", "report.md", strings.NewReader("# report"))
	require.NoError(t, err)
	assert.Equal(t, "doc-1", got.ID)
	assert.Equal(t, "report.md", got.Name)
	assert.Equal(t, int64(12), got.Size)
	assert.Equal(t, "UNSTART", got.Run)
}

// TestClientRetrievalIncludesDatasetIDs 验证检索请求显式限制 dataset_ids，避免 runtime 端选择外部知识库。
func TestClientRetrievalIncludesDatasetIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/retrieval", r.URL.Path)

		var body struct {
			DatasetIDs []string `json:"dataset_ids"`
			Question   string   `json:"question"`
			TopK       int32    `json:"top_k"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, []string{"app-ds", "org-ds"}, body.DatasetIDs)
		assert.Equal(t, "退款政策", body.Question)
		assert.Equal(t, int32(8), body.TopK)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"chunks":[
			{"id":"c1","content":"实例退款政策","document_id":"d1","document_name":"app.md","dataset_id":"app-ds","similarity":0.9},
			{"id":"c2","content":"组织退款政策","document_id":"d2","document_name":"org.md","dataset_id":"org-ds","similarity":0.8}
		]}}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server.URL)
	got, err := client.Retrieve(context.Background(), []string{"app-ds", "org-ds"}, "退款政策", 8)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "c1", got[0].ID)
	assert.Equal(t, "app-ds", got[0].DatasetID)
	assert.Equal(t, "c2", got[1].ID)
	assert.Equal(t, "org-ds", got[1].DatasetID)
}

// TestClientRAGFlowCodeError 验证 RAGFlow 业务错误码会带出 message，便于 service 和日志定位。
func TestClientRAGFlowCodeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":102,"message":"bad api key"}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server.URL)
	_, err := client.CreateDataset(context.Background(), "oc-org-1", "naive")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad api key")
}

// TestClientDownloadDocumentLeavesBodyOpen 验证下载接口把响应流交给调用方关闭，避免提前消费大文件。
func TestClientDownloadDocumentLeavesBodyOpen(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/datasets/ds-1/documents/doc-1", r.URL.Path)
		w.Header().Set("Content-Length", "7")
		_, _ = w.Write([]byte("content"))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server.URL)
	body, size, err := client.DownloadDocument(context.Background(), "ds-1", "doc-1")
	require.NoError(t, err)
	defer body.Close()
	assert.Equal(t, int64(7), size)
	content, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, "content", string(content))
}

func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	client, err := NewClient(baseURL, "secret", 0)
	require.NoError(t, err)
	return client
}
