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

// TestNewClientRejectsUnsupportedScheme 验证客户端边界拒绝非 HTTP 协议，避免绕过配置层时构造出不可用的 RAGFlow client。
func TestNewClientRejectsUnsupportedScheme(t *testing.T) {
	_, err := NewClient("ftp://ragflow:9380", "secret", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http")
}

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
	got, err := client.CreateDataset(context.Background(), CreateDatasetRequest{Name: "oc-org-1", ChunkMethod: "naive"})
	require.NoError(t, err)
	assert.Equal(t, Dataset{ID: "ds-1", Name: "oc-org-1"}, got)
}

// TestClientCreateDatasetIncludesEmbeddingModel 验证创建 dataset 时显式提交 manager 配置的默认 embedding 模型。
func TestClientCreateDatasetIncludesEmbeddingModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/datasets", r.URL.Path)
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "oc-org-1", body["name"])
		assert.Equal(t, "naive", body["chunk_method"])
		assert.Equal(t, "BAAI/bge-m3", body["embedding_model"])
		_, _ = w.Write([]byte(`{"code":0,"data":{"id":"ds-1","name":"oc-org-1"}}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server.URL)
	got, err := client.CreateDataset(context.Background(), CreateDatasetRequest{
		Name: "oc-org-1", ChunkMethod: "naive", EmbeddingModel: "BAAI/bge-m3",
	})
	require.NoError(t, err)
	assert.Equal(t, "ds-1", got.ID)
}

// TestClientGetDatasetDecodesEmbeddingFields 验证 dataset detail 会保留 RAGFlow 当前 embedding 模型和统计信息。
func TestClientGetDatasetDecodesEmbeddingFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/datasets/ds-1", r.URL.Path)
		_, _ = w.Write([]byte(`{"code":0,"data":{"id":"ds-1","name":"oc-org","embd_id":"BAAI/bge-m3___OpenAI-API@OpenAI-API-Compatible","tenant_embd_id":"tenant-embd","parser_id":"naive","doc_num":2,"chunk_num":15}}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server.URL)
	got, err := client.GetDataset(context.Background(), "ds-1")
	require.NoError(t, err)
	assert.Equal(t, "BAAI/bge-m3___OpenAI-API@OpenAI-API-Compatible", got.EmbeddingModelID)
	assert.Equal(t, int32(2), got.DocNum)
	assert.Equal(t, int32(15), got.ChunkNum)
}

// TestClientUpdateDatasetEmbeddingModel 验证修改 embedding 模型时只提交 RAGFlow 需要的字段。
func TestClientUpdateDatasetEmbeddingModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		assert.Equal(t, "/api/v1/datasets/ds-1", r.URL.Path)
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "BAAI/bge-m3", body["embedding_model"])
		_, _ = w.Write([]byte(`{"code":0,"data":null}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server.URL)
	require.NoError(t, client.UpdateDatasetEmbeddingModel(context.Background(), "ds-1", "BAAI/bge-m3"))
}

// TestClientRunDatasetEmbedding 验证整库 embedding 重跑调用 RAGFlow 官方 endpoint。
func TestClientRunDatasetEmbedding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/datasets/ds-1/embedding", r.URL.Path)
		_, _ = w.Write([]byte(`{"code":0,"data":null}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server.URL)
	require.NoError(t, client.RunDatasetEmbedding(context.Background(), "ds-1"))
}

// TestClientUploadDocumentUsesMultipart 验证上传文档使用 RAGFlow 要求的 multipart file 字段，
// 并兼容官方接口返回 data 数组的 document 元数据。
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
		_, _ = w.Write([]byte(`{"code":0,"data":[{"id":"doc-1","name":"report.md","size":12,"run":"UNSTART"}]}`))
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

// TestClientUploadDocumentRejectsMissingData 验证上传响应缺少 data 时不会返回零值文档，避免 service 层误认为上传成功。
func TestClientUploadDocumentRejectsMissingData(t *testing.T) {
	for name, response := range map[string]string{
		// 用例：RAGFlow 返回缺失 data 的成功信封时，应视为协议异常。
		"missing_data": `{"code":0}`,
		// 用例：RAGFlow 返回 null data 时，应视为协议异常。
		"null_data": `{"code":0,"data":null}`,
		// 用例：RAGFlow 返回空数组 data 时，应视为协议异常。
		"empty_data": `{"code":0,"data":[]}`,
		// 用例：RAGFlow 返回空对象 data 时，应视为协议异常。
		"empty_document": `{"code":0,"data":{}}`,
		// 用例：RAGFlow 返回空文档数组时，应视为协议异常。
		"empty_document_array": `{"code":0,"data":[{}]}`,
		// 用例：RAGFlow 返回缺少远端 document id 的文档时，service 无法持久化映射，应视为协议异常。
		"missing_document_id": `{"code":0,"data":[{"name":"report.md"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(response))
			}))
			t.Cleanup(server.Close)

			client := newTestClient(t, server.URL)
			_, err := client.UploadDocument(context.Background(), "ds-1", "report.md", strings.NewReader("# report"))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "响应")
		})
	}
}

// TestClientListDocumentsUsesTotalDatasetsFallback 验证兼容 RAGFlow 文档列表响应中的 total_datasets 总数字段。
func TestClientListDocumentsUsesTotalDatasetsFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/datasets/ds-1/documents", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"docs":[{"id":"doc-1","name":"report.md"}],"total_datasets":7}}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server.URL)
	docs, total, err := client.ListDocuments(context.Background(), "ds-1", 1, 10, "", "")
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.Equal(t, "doc-1", docs[0].ID)
	assert.Equal(t, int32(7), total)
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

// TestClientRetrievalDecodesOfficialChunkFields 验证兼容 RAGFlow 官方 retrieval 字段名，dataset 和文档名不会丢失。
func TestClientRetrievalDecodesOfficialChunkFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{
			"chunks":[
				{"id":"c1","content":"退款政策","document_id":"d1","document_keyword":"report.md","kb_id":"ds-1","similarity":0.91},
				{"id":"c2","content":"售后政策","document_id":"d2","kb_id":"ds-2","similarity":0.82}
			],
			"doc_aggs":[
				{"doc_id":"d1","doc_name":"report-from-agg.md"},
				{"doc_id":"d2","doc_name":"after-sale.md"}
			]
		}}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server.URL)
	got, err := client.Retrieve(context.Background(), []string{"ds-1", "ds-2"}, "退款政策", 5)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "ds-1", got[0].DatasetID)
	assert.Equal(t, "report.md", got[0].DocumentName)
	assert.Equal(t, "ds-2", got[1].DatasetID)
	assert.Equal(t, "after-sale.md", got[1].DocumentName)
}

// TestClientRAGFlowCodeError 验证 RAGFlow 业务错误码会带出 message，便于 service 和日志定位。
func TestClientRAGFlowCodeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":102,"message":"bad api key"}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server.URL)
	_, err := client.CreateDataset(context.Background(), CreateDatasetRequest{Name: "oc-org-1", ChunkMethod: "naive"})
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
