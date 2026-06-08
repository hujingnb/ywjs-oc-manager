# RAGFlow Embedding Model Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让平台管理员在行业知识库、企业知识库、实例知识库中查看 RAGFlow dataset 信息，并可修改 embedding 模型后触发整库重新解析。

**Architecture:** 后端以 `KnowledgeService` 作为统一入口，实时读取 RAGFlow dataset 和 embedding 模型信息，不在 manager 数据库保存模型快照。RAGFlow client 负责协议适配和模型标识归一化，service 负责权限、scope 定位、重解析和本地文件状态回到 `queued`。前端复用一个 `RAGFlowDatasetInfoDialog`，三处页面只传 scope、targetId 和缓存刷新回调。

**Tech Stack:** Go + Gin + sqlc + MySQL，RAGFlow HTTP API，Vue 3 + TypeScript + TanStack Vue Query + Naive UI，Swag/OpenAPI，Vitest，真实浏览器验证。

---

## File Structure

- Modify: `internal/config/config.go`  
  增加 `RAGFlowEmbeddingModelConfig`，配置默认 embedding 模型和兜底模型列表。
- Modify: `internal/config/loader.go`  
  校验 `ragflow.default_embedding_model` 与 `ragflow.embedding_models`，RAGFlow 未启用时允许为空。
- Modify: `internal/config/loader_test.go`  
  覆盖默认模型配置、空白模型名和重复模型配置。
- Modify: `internal/integrations/ragflow/client.go`  
  新增 dataset detail、dataset embedding 更新、整库 embedding 重跑、模型列表方法；创建 dataset 支持 `embedding_model`。
- Modify: `internal/integrations/ragflow/client_test.go`  
  覆盖新增 RAGFlow client 请求体、路径、响应解码和 fallback 兼容。
- Modify: `internal/auth/authorizer.go`  
  新增平台管理员专用 RAGFlow 知识库运维权限谓词。
- Modify: `internal/store/queries/ragflow_knowledge.sql`  
  新增批量重置某 dataset 下全部 document 解析状态的 sqlc query。
- Regenerate: `internal/store/sqlc/ragflow_knowledge.sql.go`, `internal/store/sqlc/querier.go`  
  由 `make sqlc-generate` 生成。
- Modify: `internal/service/knowledge_service.go`  
  增加统一 RAGFlow dataset 信息读取、embedding 模型列表、embedding 模型修改和本地状态重置流程。
- Modify: `internal/service/industry_knowledge_service.go`  
  只在需要复用行业库 target name helper 时补充小型 helper，避免复制行业库读取逻辑。
- Modify: `internal/service/knowledge_service_test.go`  
  覆盖权限、not_created、RAGFlow 读取失败、模型修改成功、模型更新失败和重解析失败。
- Modify: `internal/api/handlers/dto.go`  
  增加 `UpdateKnowledgeEmbeddingModelRequest` 请求体。
- Modify: `internal/api/handlers/knowledge.go`  
  注册企业/实例 RAGFlow dataset 信息和模型修改接口。
- Modify: `internal/api/handlers/industry_knowledge.go`  
  注册行业知识库 RAGFlow dataset 信息和模型修改接口。
- Modify: `internal/api/handlers/knowledge_test.go`, `internal/api/handlers/industry_knowledge_test.go`  
  覆盖 handler 路由、请求体绑定和错误映射。
- Modify: `cmd/server/main.go`  
  注入默认 embedding 模型配置和兜底模型列表。
- Regenerate: `openapi/openapi.yaml`, `web/src/api/generated.ts`  
  由 `make openapi-gen` 和 `make web-types-gen` 生成。
- Modify: `web/src/domain/permissions.ts`, `web/src/domain/permissions.spec.ts`  
  新增 `canManageRAGFlowDatasetInfo`，仅平台管理员为 true。
- Modify: `web/src/api/hooks/useKnowledge.ts`  
  增加共享 RAGFlow dataset info/model hooks，企业与实例共用。
- Modify: `web/src/api/hooks/useIndustryKnowledge.ts`  
  增加行业知识库 RAGFlow dataset info/model hooks，复用共享类型。
- Create: `web/src/components/RAGFlowDatasetInfoDialog.vue`  
  统一弹框，负责展示 dataset 信息、模型下拉、二次确认和提交状态。
- Create: `web/src/components/RAGFlowDatasetInfoDialog.spec.ts`  
  覆盖加载、错误、not_created、模型修改确认和成功事件。
- Modify: `web/src/pages/platform/IndustryKnowledgePage.vue`, `web/src/pages/platform/IndustryKnowledgePage.spec.ts`  
  行业库行操作增加 `RAGFlow 信息`。
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.vue`, `web/src/pages/knowledge/OrgKnowledgePage.spec.ts`  
  企业知识库 header 增加平台管理员可见入口。
- Modify: `web/src/pages/apps/AppKnowledgeTab.vue`, `web/src/pages/apps/AppKnowledgeTab.spec.ts`  
  实例知识库 header 增加平台管理员可见入口。

## Implementation Tasks

### Task 1: Config And Auth Predicate

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/loader.go`
- Modify: `internal/config/loader_test.go`
- Modify: `internal/auth/authorizer.go`
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Write config tests**

Add tests near the existing RAGFlow config tests in `internal/config/loader_test.go`:

```go
// TestLoad_RAGFlowEmbeddingModelsAcceptHumanNames 验证 embedding 模型配置只需要填写 RAGFlow 控制台可见的人类模型名。
func TestLoad_RAGFlowEmbeddingModelsAcceptHumanNames(t *testing.T) {
	yaml := fullValidYAML() + `
ragflow:
  base_url: "http://ragflow:9380"
  api_key: "secret"
  default_embedding_model: "BAAI/bge-m3"
  embedding_models:
    - name: "BAAI/bge-m3"
      label: "BAAI/bge-m3"
      provider: "OpenAI-API-Compatible"
    - name: "netease-youdao/bce-embedding-base_v1"
      provider: "OpenAI-API-Compatible"
`
	cfg := loadConfigFromString(t, yaml)
	require.Len(t, cfg.RAGFlow.EmbeddingModels, 2)
	assert.Equal(t, "BAAI/bge-m3", cfg.RAGFlow.DefaultEmbeddingModel)
	assert.Equal(t, "BAAI/bge-m3", cfg.RAGFlow.EmbeddingModels[0].Name)
	assert.Equal(t, "BAAI/bge-m3", cfg.RAGFlow.EmbeddingModels[0].Label)
	assert.Equal(t, "OpenAI-API-Compatible", cfg.RAGFlow.EmbeddingModels[0].Provider)
}

// TestLoad_RAGFlowEmbeddingModelRejectsBlankName 验证启用 RAGFlow 后兜底模型名不能为空，避免创建 dataset 时提交空模型。
func TestLoad_RAGFlowEmbeddingModelRejectsBlankName(t *testing.T) {
	yaml := fullValidYAML() + `
ragflow:
  base_url: "http://ragflow:9380"
  api_key: "secret"
  default_embedding_model: "BAAI/bge-m3"
  embedding_models:
    - name: "   "
      provider: "OpenAI-API-Compatible"
`
	_, err := loadConfigFromStringErr(t, yaml)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ragflow.embedding_models[0].name")
}

// TestLoad_RAGFlowDefaultEmbeddingModelMustExistInFallbackList 验证默认模型必须出现在兜底列表中，避免 RAGFlow 模型列表不可用时无法解析默认值。
func TestLoad_RAGFlowDefaultEmbeddingModelMustExistInFallbackList(t *testing.T) {
	yaml := fullValidYAML() + `
ragflow:
  base_url: "http://ragflow:9380"
  api_key: "secret"
  default_embedding_model: "missing-model"
  embedding_models:
    - name: "BAAI/bge-m3"
      provider: "OpenAI-API-Compatible"
`
	_, err := loadConfigFromStringErr(t, yaml)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ragflow.default_embedding_model")
}
```

- [ ] **Step 2: Run config tests and verify failure**

Run:

```bash
rtk go test ./internal/config -run 'TestLoad_RAGFlowEmbedding' -count=1
```

Expected: fails because `RAGFlowConfig` does not yet have `DefaultEmbeddingModel` and `EmbeddingModels`.

- [ ] **Step 3: Add config structs**

Extend `internal/config/config.go`:

```go
type RAGFlowConfig struct {
	BaseURL string `yaml:"base_url"`
	APIKey string `yaml:"api_key"`
	RequestTimeout Duration `yaml:"request_timeout"`
	ChunkMethod string `yaml:"chunk_method"`
	// DefaultEmbeddingModel 是新建 RAGFlow dataset 时使用的人类可读 embedding 模型名。
	DefaultEmbeddingModel string `yaml:"default_embedding_model"`
	// EmbeddingModels 是 RAGFlow 模型列表接口不可用时的兜底候选项，不保存供应商密钥。
	EmbeddingModels []RAGFlowEmbeddingModelConfig `yaml:"embedding_models"`
}

// RAGFlowEmbeddingModelConfig 描述 manager 可展示和提交的 embedding 模型候选。
type RAGFlowEmbeddingModelConfig struct {
	// Name 是 RAGFlow 控制台创建模型时填写的人类可读模型名。
	Name string `yaml:"name"`
	// Label 是前端展示名；为空时后端回退到 Name。
	Label string `yaml:"label"`
	// Provider 是 RAGFlow factory/provider 名称，用于区分同名模型来源。
	Provider string `yaml:"provider"`
}
```

- [ ] **Step 4: Add config validation**

Extend `RAGFlowConfig.validate()` in `internal/config/loader.go`:

```go
	if err := r.validateEmbeddingModels(); err != nil {
		return err
	}
	return nil
}

// validateEmbeddingModels 校验 manager 侧可见的 embedding 模型候选；未启用 RAGFlow 时允许完全为空。
func (r RAGFlowConfig) validateEmbeddingModels() error {
	if strings.TrimSpace(r.BaseURL) == "" && strings.TrimSpace(r.APIKey) == "" {
		return nil
	}
	defaultName := strings.TrimSpace(r.DefaultEmbeddingModel)
	seen := map[string]struct{}{}
	hasDefault := defaultName == ""
	for index, model := range r.EmbeddingModels {
		name := strings.TrimSpace(model.Name)
		provider := strings.TrimSpace(model.Provider)
		if name == "" {
			return fmt.Errorf("ragflow.embedding_models[%d].name 不能为空", index)
		}
		key := name + "\x00" + provider
		if _, ok := seen[key]; ok {
			return fmt.Errorf("ragflow.embedding_models[%d] 重复配置: %s", index, name)
		}
		seen[key] = struct{}{}
		if name == defaultName {
			hasDefault = true
		}
	}
	if defaultName != "" && !hasDefault {
		return fmt.Errorf("ragflow.default_embedding_model 必须存在于 ragflow.embedding_models")
	}
	return nil
}
```

- [ ] **Step 5: Add auth predicate**

Add to `internal/auth/authorizer.go` near knowledge predicates:

```go
// CanManageKnowledgeRAGFlowDataset 判断主体是否可查看和修改知识库对应的 RAGFlow dataset 运维信息。
// 该能力会暴露远端 dataset ID 并触发整库重解析，只允许平台管理员使用。
func CanManageKnowledgeRAGFlowDataset(p Principal) bool {
	return p.Role == domain.UserRolePlatformAdmin
}
```

- [ ] **Step 6: Wire config into service**

After creating `knowledgeService` in `cmd/server/main.go`, add:

```go
	knowledgeService.SetDefaultEmbeddingModel(cfg.RAGFlow.DefaultEmbeddingModel)
	knowledgeService.SetEmbeddingModelFallbacks(cfg.RAGFlow.EmbeddingModels)
```

- [ ] **Step 7: Run config/auth tests**

Run:

```bash
rtk go test ./internal/config ./internal/auth -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
rtk git add internal/config/config.go internal/config/loader.go internal/config/loader_test.go internal/auth/authorizer.go cmd/server/main.go
rtk git commit -m "feat(knowledge): 增加 RAGFlow 模型配置"
```

Commit body:

```text
为知识库 RAGFlow dataset 创建流程增加默认 embedding 模型和兜底模型列表配置。

配置只填写 RAGFlow 控制台可见的模型名和 provider，内部模型 ID 由后端运行时解析。
```

### Task 2: RAGFlow Client Protocol Support

**Files:**
- Modify: `internal/integrations/ragflow/client.go`
- Modify: `internal/integrations/ragflow/client_test.go`
- Modify: `internal/service/knowledge_service.go`
- Modify: `internal/service/knowledge_service_test.go`

- [ ] **Step 1: Write RAGFlow client tests**

Add tests to `internal/integrations/ragflow/client_test.go`:

```go
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
```

- [ ] **Step 2: Run RAGFlow client tests and verify failure**

Run:

```bash
rtk go test ./internal/integrations/ragflow -run 'TestClient(CreateDatasetIncludesEmbeddingModel|GetDatasetDecodesEmbeddingFields|UpdateDatasetEmbeddingModel|RunDatasetEmbedding)' -count=1
```

Expected: fails because the new request type and methods are missing.

- [ ] **Step 3: Add client request/result types**

In `internal/integrations/ragflow/client.go`, replace the small `Dataset` struct with:

```go
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
	ID              string `json:"id"`
	Name            string `json:"name"`
	EmbeddingModelID string `json:"embd_id"`
	TenantEmbeddingID string `json:"tenant_embd_id"`
	ParserID        string `json:"parser_id"`
	DocNum          int32  `json:"doc_num"`
	ChunkNum        int32  `json:"chunk_num"`
}

// EmbeddingModel 描述 RAGFlow 可用 embedding 模型；InternalID 仅后端提交 RAGFlow 时使用。
type EmbeddingModel struct {
	Name       string
	Label      string
	Provider   string
	InternalID string
	Available  bool
}
```

- [ ] **Step 4: Update client methods**

Replace `CreateDataset` and add new methods in `client.go`:

```go
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
	var out Dataset
	if err := c.doJSON(ctx, http.MethodGet, c.apiPath("/api/v1/datasets", datasetID), nil, nil, &out); err != nil {
		return Dataset{}, err
	}
	return out, nil
}

// UpdateDatasetEmbeddingModel 修改 RAGFlow dataset 的 embedding 模型。
func (c *Client) UpdateDatasetEmbeddingModel(ctx context.Context, datasetID, embeddingModel string) error {
	body := map[string]string{"embedding_model": strings.TrimSpace(embeddingModel)}
	return c.doJSON(ctx, http.MethodPut, c.apiPath("/api/v1/datasets", datasetID), nil, body, nil)
}

// RunDatasetEmbedding 触发指定 dataset 下全部文件重新 embedding。
func (c *Client) RunDatasetEmbedding(ctx context.Context, datasetID string) error {
	return c.doJSON(ctx, http.MethodPost, c.apiPath("/api/v1/datasets", datasetID, "embedding"), nil, nil, nil)
}
```

- [ ] **Step 5: Keep service interface compiling**

Update `RAGFlowKnowledgeClient` in `internal/service/knowledge_service.go`:

```go
	CreateDataset(ctx context.Context, req ragflow.CreateDatasetRequest) (ragflow.Dataset, error)
	GetDataset(ctx context.Context, datasetID string) (ragflow.Dataset, error)
	UpdateDatasetEmbeddingModel(ctx context.Context, datasetID, embeddingModel string) error
	RunDatasetEmbedding(ctx context.Context, datasetID string) error
```

Update fake and missing clients in `internal/service/knowledge_service_test.go` to match the new signature:

```go
func (f *fakeRAGFlowKnowledgeClient) CreateDataset(_ context.Context, req ragflow.CreateDatasetRequest) (ragflow.Dataset, error) {
	f.createDatasetCalls = append(f.createDatasetCalls, ragflowCreateDatasetCall{
		name: req.Name, chunkMethod: req.ChunkMethod, embeddingModel: req.EmbeddingModel,
	})
	if f.createDatasetResult.ID == "" {
		return ragflow.Dataset{ID: "created-ds", Name: req.Name}, nil
	}
	return f.createDatasetResult, nil
}
```

- [ ] **Step 6: Run client and service compile tests**

Run:

```bash
rtk go test ./internal/integrations/ragflow ./internal/service -run 'TestClient|TestEnsureOrgDatasetCreatesRemoteDatasetMapping' -count=1
```

Expected: PASS after updating fakes.

- [ ] **Step 7: Commit**

```bash
rtk git add internal/integrations/ragflow/client.go internal/integrations/ragflow/client_test.go internal/service/knowledge_service.go internal/service/knowledge_service_test.go
rtk git commit -m "feat(knowledge): 扩展 RAGFlow dataset 协议"
```

Commit body:

```text
RAGFlow client 支持创建时传入 embedding_model，并新增 dataset detail、模型更新和整库 embedding 重跑接口。

service 依赖接口同步切换为结构化创建参数，为默认模型和修改模型流程做准备。
```

### Task 3: Model Resolution And Dataset Service

**Files:**
- Modify: `internal/store/queries/ragflow_knowledge.sql`
- Regenerate: `internal/store/sqlc/ragflow_knowledge.sql.go`
- Regenerate: `internal/store/sqlc/querier.go`
- Modify: `internal/service/knowledge_service.go`
- Modify: `internal/service/industry_knowledge_service.go`
- Modify: `internal/service/knowledge_service_test.go`

- [ ] **Step 1: Add sqlc reset query**

Append to `internal/store/queries/ragflow_knowledge.sql` after `UpdateRAGFlowDocumentParseStatus`:

```sql
-- name: ResetRAGFlowDocumentsParseStatusByDataset :exec
-- 整库 embedding 模型切换后，把该 dataset 下所有本地 document 状态重置为 queued，交给现有刷新任务继续推进。
UPDATE ragflow_documents
SET parse_status = 'queued',
    progress = 0,
    last_error = NULL,
    updated_at = now()
WHERE dataset_id = ?;
```

- [ ] **Step 2: Regenerate sqlc**

Run:

```bash
rtk make sqlc-generate
```

Expected: `ResetRAGFlowDocumentsParseStatusByDataset(ctx, datasetID string) error` appears in `internal/store/sqlc/querier.go`.

- [ ] **Step 3: Write service tests**

Add tests in `internal/service/knowledge_service_test.go`:

```go
// TestKnowledgeRAGFlowDatasetInfoOnlyPlatformAdmin 验证 RAGFlow dataset 运维信息只允许平台管理员读取。
func TestKnowledgeRAGFlowDatasetInfoOnlyPlatformAdmin(t *testing.T) {
	svc, _, _ := newRAGFlowKnowledgeTestService(t)
	_, err := svc.GetKnowledgeRAGFlowDatasetInfo(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testKnowledgeOrg}, KnowledgeRAGFlowScopeOrg, testKnowledgeOrg)
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

// TestKnowledgeRAGFlowDatasetInfoReturnsNotCreated 验证查看 RAGFlow 信息不会懒创建 dataset。
func TestKnowledgeRAGFlowDatasetInfoReturnsNotCreated(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	store.missingOrgDataset = true
	result, err := svc.GetKnowledgeRAGFlowDatasetInfo(context.Background(), platformKnowledgePrincipal(), KnowledgeRAGFlowScopeOrg, testKnowledgeOrg)
	require.NoError(t, err)
	assert.Equal(t, "not_created", result.Status)
	assert.Empty(t, rf.createDatasetCalls)
}

// TestUpdateKnowledgeEmbeddingModelResetsLocalDocuments 验证模型修改和整库重解析成功后，本地文件全部回到 queued。
func TestUpdateKnowledgeEmbeddingModelResetsLocalDocuments(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	svc.SetEmbeddingModelFallbacks([]config.RAGFlowEmbeddingModelConfig{{Name: "BAAI/bge-m3", Label: "BAAI/bge-m3", Provider: "OpenAI-API-Compatible"}})
	store.docs["doc-a"] = makeKnowledgeDoc("doc-a", store.orgDataset.ID, "remote-a", "completed", 100)
	store.docs["doc-b"] = makeKnowledgeDoc("doc-b", store.orgDataset.ID, "remote-b", "failed", 42)
	rf.datasetDetail = ragflow.Dataset{ID: testRemoteOrgDatasetID, Name: "oc-org", EmbeddingModelID: "BAAI/bge-m3___OpenAI-API@OpenAI-API-Compatible", DocNum: 2, ChunkNum: 9}

	result, err := svc.UpdateKnowledgeEmbeddingModel(context.Background(), platformKnowledgePrincipal(), KnowledgeRAGFlowScopeOrg, testKnowledgeOrg, KnowledgeEmbeddingModelInput{Name: "BAAI/bge-m3", Provider: "OpenAI-API-Compatible"})
	require.NoError(t, err)
	assert.Equal(t, "ok", result.Status)
	assert.Equal(t, []string{testRemoteOrgDatasetID}, rf.runDatasetEmbeddingCalls)
	assert.Equal(t, "queued", store.docs["doc-a"].ParseStatus)
	assert.Equal(t, int32(0), store.docs["doc-a"].Progress)
	assert.False(t, store.docs["doc-a"].LastError.Valid)
	assert.Equal(t, "queued", store.docs["doc-b"].ParseStatus)
}

// TestUpdateKnowledgeEmbeddingModelDoesNotResetWhenReparseFails 验证 RAGFlow 未接受整库重解析时不改本地状态，避免 UI 误报解析中。
func TestUpdateKnowledgeEmbeddingModelDoesNotResetWhenReparseFails(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	svc.SetEmbeddingModelFallbacks([]config.RAGFlowEmbeddingModelConfig{{Name: "BAAI/bge-m3", Provider: "OpenAI-API-Compatible"}})
	store.docs["doc-a"] = makeKnowledgeDoc("doc-a", store.orgDataset.ID, "remote-a", "completed", 100)
	rf.runDatasetEmbeddingErr = errors.New("ragflow busy")

	_, err := svc.UpdateKnowledgeEmbeddingModel(context.Background(), platformKnowledgePrincipal(), KnowledgeRAGFlowScopeOrg, testKnowledgeOrg, KnowledgeEmbeddingModelInput{Name: "BAAI/bge-m3", Provider: "OpenAI-API-Compatible"})
	require.Error(t, err)
	assert.Equal(t, "completed", store.docs["doc-a"].ParseStatus)
	assert.Equal(t, int32(100), store.docs["doc-a"].Progress)
}
```

- [ ] **Step 4: Run service tests and verify failure**

Run:

```bash
rtk go test ./internal/service -run 'TestKnowledgeRAGFlowDatasetInfo|TestUpdateKnowledgeEmbeddingModel' -count=1
```

Expected: fails because the service result/input types and methods are missing.

- [ ] **Step 5: Add service result/input types**

In `internal/service/knowledge_service.go`, add near result types:

```go
const (
	KnowledgeRAGFlowScopeOrg      = "org"
	KnowledgeRAGFlowScopeApp      = "app"
	KnowledgeRAGFlowScopeIndustry = "industry"
)

type KnowledgeEmbeddingModelResult struct {
	Name       string `json:"name"`
	Label      string `json:"label"`
	Provider   string `json:"provider"`
	Available  bool   `json:"available"`
}

type KnowledgeEmbeddingModelInput struct {
	Name     string
	Provider string
}

type KnowledgeEmbeddingModelListResult struct {
	Items []KnowledgeEmbeddingModelResult `json:"items"`
}

type KnowledgeRAGFlowDatasetInfoResult struct {
	Scope              string                         `json:"scope"`
	TargetID           string                         `json:"target_id"`
	TargetName         string                         `json:"target_name"`
	Status             string                         `json:"status"`
	RAGFlowDatasetID    string                         `json:"ragflow_dataset_id,omitempty"`
	RAGFlowDatasetName  string                         `json:"ragflow_dataset_name,omitempty"`
	EmbeddingModel      *KnowledgeEmbeddingModelResult `json:"embedding_model,omitempty"`
	ErrorMessage        string                         `json:"error_message,omitempty"`
	DocNum              int32                          `json:"doc_num,omitempty"`
	ChunkNum            int32                          `json:"chunk_num,omitempty"`
	UpdatedAt           string                         `json:"updated_at,omitempty"`
}
```

- [ ] **Step 6: Add service configuration setters**

Extend `KnowledgeService`:

```go
	defaultEmbeddingModel string
	embeddingModelFallbacks []config.RAGFlowEmbeddingModelConfig
```

Add methods:

```go
// SetDefaultEmbeddingModel 设置新建 RAGFlow dataset 时显式使用的默认 embedding 模型。
func (s *KnowledgeService) SetDefaultEmbeddingModel(model string) {
	s.defaultEmbeddingModel = strings.TrimSpace(model)
}

// SetEmbeddingModelFallbacks 设置 RAGFlow 模型列表接口失败时使用的展示和校验兜底项。
func (s *KnowledgeService) SetEmbeddingModelFallbacks(models []config.RAGFlowEmbeddingModelConfig) {
	s.embeddingModelFallbacks = append([]config.RAGFlowEmbeddingModelConfig(nil), models...)
}
```

- [ ] **Step 7: Use default model when creating datasets**

In `createRemoteDataset`, replace the client call with:

```go
	remote, err := s.ragflowClient().CreateDataset(ctx, ragflow.CreateDatasetRequest{
		Name:           dataset.Name,
		ChunkMethod:    s.datasetChunkMethod,
		EmbeddingModel: s.defaultEmbeddingModel,
	})
```

Add or update `TestEnsureOrgDatasetCreatesRemoteDatasetMapping` to assert:

```go
svc.SetDefaultEmbeddingModel("BAAI/bge-m3")
assert.Equal(t, "BAAI/bge-m3", rf.createDatasetCalls[0].embeddingModel)
```

- [ ] **Step 8: Implement dataset info and update flow**

Add public service methods:

```go
func (s *KnowledgeService) ListKnowledgeEmbeddingModels(ctx context.Context, principal auth.Principal) (KnowledgeEmbeddingModelListResult, error) {
	if !auth.CanManageKnowledgeRAGFlowDataset(principal) {
		return KnowledgeEmbeddingModelListResult{}, ErrKnowledgeForbidden
	}
	return KnowledgeEmbeddingModelListResult{Items: s.embeddingModelResultsFromFallbacks()}, nil
}

func (s *KnowledgeService) GetKnowledgeRAGFlowDatasetInfo(ctx context.Context, principal auth.Principal, scope, targetID string) (KnowledgeRAGFlowDatasetInfoResult, error) {
	if !auth.CanManageKnowledgeRAGFlowDataset(principal) {
		return KnowledgeRAGFlowDatasetInfoResult{}, ErrKnowledgeForbidden
	}
	target, dataset, err := s.resolveKnowledgeRAGFlowTarget(ctx, scope, targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return KnowledgeRAGFlowDatasetInfoResult{Scope: scope, TargetID: targetID, TargetName: target.Name, Status: "not_created"}, nil
	}
	if err != nil {
		return KnowledgeRAGFlowDatasetInfoResult{}, err
	}
	remoteID, err := requireRemoteDatasetID(dataset)
	if err != nil {
		return KnowledgeRAGFlowDatasetInfoResult{Scope: scope, TargetID: targetID, TargetName: target.Name, Status: "not_created"}, nil
	}
	remote, err := s.ragflowClient().GetDataset(ctx, remoteID)
	if err != nil {
		return KnowledgeRAGFlowDatasetInfoResult{Scope: scope, TargetID: targetID, TargetName: target.Name, Status: "error", ErrorMessage: err.Error()}, nil
	}
	return s.toKnowledgeRAGFlowDatasetInfo(scope, targetID, target.Name, dataset, remote), nil
}

func (s *KnowledgeService) UpdateKnowledgeEmbeddingModel(ctx context.Context, principal auth.Principal, scope, targetID string, input KnowledgeEmbeddingModelInput) (KnowledgeRAGFlowDatasetInfoResult, error) {
	if !auth.CanManageKnowledgeRAGFlowDataset(principal) {
		return KnowledgeRAGFlowDatasetInfoResult{}, ErrKnowledgeForbidden
	}
	target, dataset, err := s.resolveKnowledgeRAGFlowTarget(ctx, scope, targetID)
	if err != nil {
		return KnowledgeRAGFlowDatasetInfoResult{}, err
	}
	remoteID, err := requireRemoteDatasetID(dataset)
	if err != nil {
		return KnowledgeRAGFlowDatasetInfoResult{}, err
	}
	model, err := s.resolveKnowledgeEmbeddingModel(input)
	if err != nil {
		return KnowledgeRAGFlowDatasetInfoResult{}, err
	}
	if err := s.ragflowClient().UpdateDatasetEmbeddingModel(ctx, remoteID, model.Name); err != nil {
		return KnowledgeRAGFlowDatasetInfoResult{}, fmt.Errorf("更新 RAGFlow embedding 模型失败: %w", err)
	}
	if err := s.ragflowClient().RunDatasetEmbedding(ctx, remoteID); err != nil {
		return KnowledgeRAGFlowDatasetInfoResult{}, fmt.Errorf("触发 RAGFlow 整库重新解析失败: %w", err)
	}
	if err := s.store.ResetRAGFlowDocumentsParseStatusByDataset(ctx, dataset.ID); err != nil {
		return KnowledgeRAGFlowDatasetInfoResult{}, fmt.Errorf("重置知识库文件解析状态失败: %w", err)
	}
	remote, err := s.ragflowClient().GetDataset(ctx, remoteID)
	if err != nil {
		return KnowledgeRAGFlowDatasetInfoResult{Scope: scope, TargetID: targetID, TargetName: target.Name, Status: "error", ErrorMessage: err.Error()}, nil
	}
	return s.toKnowledgeRAGFlowDatasetInfo(scope, targetID, target.Name, dataset, remote), nil
}
```

The helper `resolveKnowledgeRAGFlowTarget` must:
- For `org`: call `getOrg(ctx, targetID)` then `store.GetRAGFlowOrgDataset(ctx, null.StringFrom(org.ID))`.
- For `app`: call `getApp(ctx, targetID)` then `store.GetRAGFlowAppDataset(ctx, null.StringFrom(app.ID))`.
- For `industry`: call `getIndustryKnowledgeBase(ctx, targetID)` then `store.GetRAGFlowIndustryDataset(ctx, null.StringFrom(base.ID))`.
- Return `ErrNotFound` for unknown scope.

- [ ] **Step 9: Update fake store/client**

Extend `KnowledgeStore` and `fakeKnowledgeStore` with:

```go
	ResetRAGFlowDocumentsParseStatusByDataset(ctx context.Context, datasetID string) error
```

Fake implementation:

```go
func (s *fakeKnowledgeStore) ResetRAGFlowDocumentsParseStatusByDataset(_ context.Context, datasetID string) error {
	for id, doc := range s.docs {
		if doc.DatasetID == datasetID {
			doc.ParseStatus = "queued"
			doc.Progress = 0
			doc.LastError = null.String{}
			s.docs[id] = doc
		}
	}
	return nil
}
```

Extend fake RAGFlow client:

```go
	datasetDetail ragflow.Dataset
	updateDatasetEmbeddingCalls []ragflowUpdateDatasetEmbeddingCall
	runDatasetEmbeddingCalls []string
	runDatasetEmbeddingErr error
```

with methods:

```go
func (f *fakeRAGFlowKnowledgeClient) GetDataset(context.Context, string) (ragflow.Dataset, error) {
	if f.datasetDetail.ID == "" {
		return ragflow.Dataset{ID: testRemoteOrgDatasetID, Name: "oc-dataset"}, nil
	}
	return f.datasetDetail, nil
}

func (f *fakeRAGFlowKnowledgeClient) UpdateDatasetEmbeddingModel(_ context.Context, datasetID, embeddingModel string) error {
	f.updateDatasetEmbeddingCalls = append(f.updateDatasetEmbeddingCalls, ragflowUpdateDatasetEmbeddingCall{datasetID: datasetID, embeddingModel: embeddingModel})
	return nil
}

func (f *fakeRAGFlowKnowledgeClient) RunDatasetEmbedding(_ context.Context, datasetID string) error {
	f.runDatasetEmbeddingCalls = append(f.runDatasetEmbeddingCalls, datasetID)
	return f.runDatasetEmbeddingErr
}
```

- [ ] **Step 10: Run service tests**

Run:

```bash
rtk go test ./internal/service -run 'TestEnsureOrgDatasetCreatesRemoteDatasetMapping|TestKnowledgeRAGFlowDatasetInfo|TestUpdateKnowledgeEmbeddingModel' -count=1
```

Expected: PASS.

- [ ] **Step 11: Commit**

```bash
rtk git add internal/store/queries/ragflow_knowledge.sql internal/store/sqlc/ragflow_knowledge.sql.go internal/store/sqlc/querier.go internal/service/knowledge_service.go internal/service/industry_knowledge_service.go internal/service/knowledge_service_test.go
rtk git commit -m "feat(knowledge): 支持修改 RAGFlow embedding 模型"
```

Commit body:

```text
新增知识库 RAGFlow dataset 实时信息读取、embedding 模型修改和整库重新解析流程。

模型修改成功且 RAGFlow 接受整库重解析后，本地 document 状态统一回到 queued，由现有刷新任务继续推进。
```

### Task 4: HTTP Routes And OpenAPI

**Files:**
- Modify: `internal/api/handlers/dto.go`
- Modify: `internal/api/handlers/knowledge.go`
- Modify: `internal/api/handlers/industry_knowledge.go`
- Modify: `internal/api/handlers/knowledge_test.go`
- Modify: `internal/api/handlers/industry_knowledge_test.go`
- Regenerate: `openapi/openapi.yaml`
- Regenerate: `web/src/api/generated.ts`

- [ ] **Step 1: Add DTO**

In `internal/api/handlers/dto.go` under knowledge DTOs:

```go
// UpdateKnowledgeEmbeddingModelRequest 是平台管理员修改 RAGFlow dataset embedding 模型的请求体。
type UpdateKnowledgeEmbeddingModelRequest struct {
	// Name 是 RAGFlow 控制台可见的模型名，不使用 RAGFlow 内部拼接 ID。
	Name string `json:"name" binding:"required"`
	// Provider 是模型来源；为空时后端按 name 做唯一匹配。
	Provider string `json:"provider"`
}
```

- [ ] **Step 2: Extend handler service interfaces**

Add to `knowledgeService` and `industryKnowledgeService` interfaces:

```go
	ListKnowledgeEmbeddingModels(ctx context.Context, principal auth.Principal) (service.KnowledgeEmbeddingModelListResult, error)
	GetKnowledgeRAGFlowDatasetInfo(ctx context.Context, principal auth.Principal, scope, targetID string) (service.KnowledgeRAGFlowDatasetInfoResult, error)
	UpdateKnowledgeEmbeddingModel(ctx context.Context, principal auth.Principal, scope, targetID string, input service.KnowledgeEmbeddingModelInput) (service.KnowledgeRAGFlowDatasetInfoResult, error)
```

- [ ] **Step 3: Register routes**

In `RegisterKnowledgeRoutes`:

```go
	router.GET("/api/v1/knowledge/embedding-models", handler.ListEmbeddingModels)
	orgGroup.GET("/ragflow-dataset", handler.GetOrgRAGFlowDataset)
	orgGroup.PATCH("/ragflow-dataset/embedding-model", handler.UpdateOrgEmbeddingModel)
	appGroup.GET("/ragflow-dataset", handler.GetAppRAGFlowDataset)
	appGroup.PATCH("/ragflow-dataset/embedding-model", handler.UpdateAppEmbeddingModel)
```

In `RegisterIndustryKnowledgeRoutes`:

```go
	group.GET("/:industryId/ragflow-dataset", handler.GetIndustryRAGFlowDataset)
	group.PATCH("/:industryId/ragflow-dataset/embedding-model", handler.UpdateIndustryEmbeddingModel)
```

- [ ] **Step 4: Implement handler methods**

Use this shape in `knowledge.go`; industry methods call the same service with `KnowledgeRAGFlowScopeIndustry`:

```go
func (h *KnowledgeHandler) ListEmbeddingModels(c *gin.Context) {
	result, err := h.service.ListKnowledgeEmbeddingModels(c.Request.Context(), principalFromCtx(c))
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *KnowledgeHandler) GetOrgRAGFlowDataset(c *gin.Context) {
	h.writeRAGFlowDatasetInfo(c, service.KnowledgeRAGFlowScopeOrg, c.Param("orgId"))
}

func (h *KnowledgeHandler) UpdateOrgEmbeddingModel(c *gin.Context) {
	h.updateRAGFlowDatasetEmbeddingModel(c, service.KnowledgeRAGFlowScopeOrg, c.Param("orgId"))
}

func (h *KnowledgeHandler) writeRAGFlowDatasetInfo(c *gin.Context, scope, targetID string) {
	result, err := h.service.GetKnowledgeRAGFlowDatasetInfo(c.Request.Context(), principalFromCtx(c), scope, targetID)
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *KnowledgeHandler) updateRAGFlowDatasetEmbeddingModel(c *gin.Context, scope, targetID string) {
	var req UpdateKnowledgeEmbeddingModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "模型名称不能为空"))
		return
	}
	result, err := h.service.UpdateKnowledgeEmbeddingModel(c.Request.Context(), principalFromCtx(c), scope, targetID, service.KnowledgeEmbeddingModelInput{Name: req.Name, Provider: req.Provider})
	if err != nil {
		writeKnowledgeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, result)
}
```

- [ ] **Step 5: Add swag annotations**

Each new method needs `@Summary`, `@Description`, `@Tags knowledge` or `@Tags industry-knowledge`, `@Security BearerAuth`, path params, success responses, and `@Router` matching:

```go
// @Router /organizations/{orgId}/knowledge/ragflow-dataset [get]
// @Router /organizations/{orgId}/knowledge/ragflow-dataset/embedding-model [patch]
// @Router /apps/{appId}/knowledge/ragflow-dataset [get]
// @Router /apps/{appId}/knowledge/ragflow-dataset/embedding-model [patch]
// @Router /industry-knowledge-bases/{industryId}/ragflow-dataset [get]
// @Router /industry-knowledge-bases/{industryId}/ragflow-dataset/embedding-model [patch]
```

- [ ] **Step 6: Add handler tests**

In `knowledge_test.go`, add:

```go
// TestKnowledgeGetOrgRAGFlowDatasetRoutesToService 验证企业知识库 RAGFlow 信息接口把 scope 和 orgId 传给 service。
func TestKnowledgeGetOrgRAGFlowDatasetRoutesToService(t *testing.T) {
	stub := &knowledgeServiceStub{ragflowInfoResult: service.KnowledgeRAGFlowDatasetInfoResult{Scope: "org", TargetID: "org-1", Status: "ok"}}
	router := newKnowledgeTestRouter(t, stub)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/knowledge/ragflow-dataset", nil)
	req = withPrincipal(req, auth.Principal{UserID: "admin", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "org", stub.ragflowInfoScope)
	assert.Equal(t, "org-1", stub.ragflowInfoTargetID)
	assert.Contains(t, w.Body.String(), `"status":"ok"`)
}

// TestKnowledgePatchOrgEmbeddingModelBindsHumanModelName 验证修改接口接收人类模型名而不是 RAGFlow 内部 ID。
func TestKnowledgePatchOrgEmbeddingModelBindsHumanModelName(t *testing.T) {
	stub := &knowledgeServiceStub{ragflowInfoResult: service.KnowledgeRAGFlowDatasetInfoResult{Scope: "org", TargetID: "org-1", Status: "ok"}}
	router := newKnowledgeTestRouter(t, stub)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/organizations/org-1/knowledge/ragflow-dataset/embedding-model", strings.NewReader(`{"name":"BAAI/bge-m3","provider":"OpenAI-API-Compatible"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "admin", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, "BAAI/bge-m3", stub.embeddingInput.Name)
	assert.Equal(t, "OpenAI-API-Compatible", stub.embeddingInput.Provider)
}
```

- [ ] **Step 7: Run handler tests**

Run:

```bash
rtk go test ./internal/api/handlers -run 'TestKnowledge.*RAGFlow|TestIndustry.*RAGFlow' -count=1
```

Expected: PASS.

- [ ] **Step 8: Generate OpenAPI and web types**

Run:

```bash
rtk make openapi-gen
rtk make web-types-gen
```

Expected: `openapi/openapi.yaml` includes new routes and `web/src/api/generated.ts` includes new schemas.

- [ ] **Step 9: Commit**

```bash
rtk git add internal/api/handlers/dto.go internal/api/handlers/knowledge.go internal/api/handlers/industry_knowledge.go internal/api/handlers/knowledge_test.go internal/api/handlers/industry_knowledge_test.go openapi/openapi.yaml web/src/api/generated.ts
rtk git commit -m "feat(knowledge): 暴露 RAGFlow 模型管理接口"
```

Commit body:

```text
新增平台管理员查看 RAGFlow dataset 信息、列出 embedding 模型和修改模型的 HTTP API。

同步更新 OpenAPI 与前端生成类型，前端只提交人类可读模型名和 provider。
```

### Task 5: Frontend Shared Hooks And Dialog

**Files:**
- Modify: `web/src/domain/permissions.ts`
- Modify: `web/src/domain/permissions.spec.ts`
- Modify: `web/src/api/hooks/useKnowledge.ts`
- Modify: `web/src/api/hooks/useIndustryKnowledge.ts`
- Create: `web/src/components/RAGFlowDatasetInfoDialog.vue`
- Create: `web/src/components/RAGFlowDatasetInfoDialog.spec.ts`

- [ ] **Step 1: Add frontend permission tests**

In `web/src/domain/permissions.spec.ts`:

```ts
describe('canManageRAGFlowDatasetInfo', () => {
  it('仅平台管理员可查看和修改 RAGFlow dataset 信息', () => {
    // 平台管理员可以跨行业库、企业库和实例库执行 RAGFlow 运维操作。
    expect(canManageRAGFlowDatasetInfo({ role: 'platform_admin' })).toBe(true)
    // 企业管理员不能看到入口，避免触发整库重解析这类平台运维操作。
    expect(canManageRAGFlowDatasetInfo({ role: 'org_admin', org_id: 'org-1' })).toBe(false)
    // 普通成员没有 RAGFlow 远端信息入口。
    expect(canManageRAGFlowDatasetInfo({ role: 'org_member', org_id: 'org-1' })).toBe(false)
  })
})
```

- [ ] **Step 2: Implement frontend permission helper**

Add to `web/src/domain/permissions.ts`:

```ts
// canManageRAGFlowDatasetInfo 控制 RAGFlow dataset 运维弹框入口；后端仍是最终权限边界。
export function canManageRAGFlowDatasetInfo(user: PermissionUser | null | undefined): boolean {
  return user?.role === 'platform_admin'
}
```

- [ ] **Step 3: Add shared hook types and query keys**

In `web/src/api/hooks/useKnowledge.ts`:

```ts
export type KnowledgeRAGFlowScope = 'org' | 'app' | 'industry'

export interface KnowledgeEmbeddingModel {
  name: string
  label: string
  provider: string
  available: boolean
}

export interface KnowledgeEmbeddingModelList {
  items: KnowledgeEmbeddingModel[]
}

export interface KnowledgeRAGFlowDatasetInfo {
  scope: KnowledgeRAGFlowScope
  target_id: string
  target_name: string
  status: 'ok' | 'not_created' | 'error' | string
  ragflow_dataset_id?: string
  ragflow_dataset_name?: string
  embedding_model?: KnowledgeEmbeddingModel
  error_message?: string
  doc_num?: number
  chunk_num?: number
  updated_at?: string
}

const ragflowDatasetKey = (scope: KnowledgeRAGFlowScope, targetId: string | undefined) => ['knowledge', 'ragflow-dataset', scope, targetId] as const
const embeddingModelsKey = ['knowledge', 'embedding-models'] as const
```

- [ ] **Step 4: Add shared hooks**

Add to `web/src/api/hooks/useKnowledge.ts`:

```ts
function ragflowDatasetPath(scope: KnowledgeRAGFlowScope, targetId: string): string {
  if (scope === 'org') return `/api/v1/organizations/${targetId}/knowledge/ragflow-dataset`
  if (scope === 'app') return `/api/v1/apps/${targetId}/knowledge/ragflow-dataset`
  return `/api/v1/industry-knowledge-bases/${targetId}/ragflow-dataset`
}

export function useKnowledgeEmbeddingModelsQuery(enabled?: () => boolean) {
  return useQuery<KnowledgeEmbeddingModelList>({
    queryKey: embeddingModelsKey,
    enabled,
    queryFn: async () => apiRequest<KnowledgeEmbeddingModelList>('/api/v1/knowledge/embedding-models'),
  })
}

export function useRAGFlowDatasetInfoQuery(scope: Ref<KnowledgeRAGFlowScope>, targetId: Ref<string | undefined>, enabled?: () => boolean) {
  return useQuery<KnowledgeRAGFlowDatasetInfo | null>({
    queryKey: computed(() => ragflowDatasetKey(scope.value, targetId.value)),
    enabled: () => Boolean(targetId.value) && (enabled ? enabled() : true),
    queryFn: async () => {
      if (!targetId.value) return null
      return apiRequest<KnowledgeRAGFlowDatasetInfo>(ragflowDatasetPath(scope.value, targetId.value))
    },
  })
}

export function useUpdateRAGFlowDatasetEmbeddingModel(scope: Ref<KnowledgeRAGFlowScope>, targetId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: { name: string; provider?: string }) => {
      if (!targetId.value) throw new Error('缺少知识库 ID')
      return apiRequest<KnowledgeRAGFlowDatasetInfo>(`${ragflowDatasetPath(scope.value, targetId.value)}/embedding-model`, {
        method: 'PATCH',
        body: input,
      })
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ragflowDatasetKey(scope.value, targetId.value) })
      if (scope.value === 'org') void client.invalidateQueries({ queryKey: orgKey(targetId.value) })
      if (scope.value === 'app') void client.invalidateQueries({ queryKey: appKey(targetId.value) })
      if (scope.value === 'industry') void client.invalidateQueries({ queryKey: ['industry-knowledge-files', targetId.value] })
    },
  })
}
```

- [ ] **Step 5: Create shared dialog component**

Create `web/src/components/RAGFlowDatasetInfoDialog.vue`:

```vue
<template>
  <n-modal :show="visible" preset="card" title="RAGFlow 信息" style="width: 560px" @update:show="emitVisible">
    <div class="ragflow-info-dialog">
      <n-spin :show="infoQuery.isLoading.value || modelsQuery.isLoading.value">
        <n-alert v-if="infoQuery.error.value" type="error" :bordered="false">
          {{ infoQuery.error.value.message }}
          <template #action><n-button size="small" @click="refetchAll">重试</n-button></template>
        </n-alert>
        <n-alert v-else-if="info?.status === 'error'" type="error" :bordered="false">
          {{ info.error_message || '读取 RAGFlow 信息失败' }}
          <template #action><n-button size="small" @click="refetchAll">重试</n-button></template>
        </n-alert>
        <n-alert v-else-if="info?.status === 'not_created'" type="warning" :bordered="false">
          当前知识库尚未创建 RAGFlow dataset，上传文件或初始化完成后再查看。
        </n-alert>
        <n-descriptions v-if="info" :column="1" bordered size="small">
          <n-descriptions-item label="知识库">{{ scopeLabel }} · {{ targetName || info.target_name || targetId }}</n-descriptions-item>
          <n-descriptions-item label="RAGFlow dataset ID">{{ info.ragflow_dataset_id || '—' }}</n-descriptions-item>
          <n-descriptions-item label="RAGFlow dataset 名称">{{ info.ragflow_dataset_name || '—' }}</n-descriptions-item>
          <n-descriptions-item label="当前模型">{{ info.embedding_model?.label || info.embedding_model?.name || '—' }}</n-descriptions-item>
          <n-descriptions-item v-if="info.doc_num !== undefined" label="文档数">{{ info.doc_num }}</n-descriptions-item>
          <n-descriptions-item v-if="info.chunk_num !== undefined" label="Chunk 数">{{ info.chunk_num }}</n-descriptions-item>
        </n-descriptions>
        <n-form label-placement="top" style="margin-top: 14px" @submit.prevent="openConfirm">
          <n-form-item label="Embedding 模型">
            <n-select v-model:value="selectedModelKey" :options="modelOptions" :disabled="!canSubmit" placeholder="选择模型" />
          </n-form-item>
          <n-space justify="end">
            <n-button @click="emitVisible(false)">关闭</n-button>
            <n-button type="primary" attr-type="submit" :disabled="!canSubmit || selectedModelUnchanged">保存并重新解析</n-button>
          </n-space>
        </n-form>
      </n-spin>
    </div>
    <ConfirmActionModal
      :visible="confirmOpen"
      title="确认修改 RAGFlow 模型"
      message="将更新 RAGFlow dataset 的 embedding 模型，并使该知识库下全部文件重新进入解析流程。"
      confirm-label="确认修改"
      verify-value="重新解析"
      verify-hint='输入 "重新解析" 以确认'
      :busy="mutation.isPending.value"
      @confirm="submit"
      @cancel="confirmOpen = false"
    />
  </n-modal>
</template>
```

The script must use `useRAGFlowDatasetInfoQuery`, `useKnowledgeEmbeddingModelsQuery`, and `useUpdateRAGFlowDatasetEmbeddingModel`; emit `updated` after mutation success.

- [ ] **Step 6: Add dialog tests**

Create `web/src/components/RAGFlowDatasetInfoDialog.spec.ts` with mocked hooks:

```ts
import { mount } from '@vue/test-utils'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { ref } from 'vue'

import RAGFlowDatasetInfoDialog from './RAGFlowDatasetInfoDialog.vue'

const info = ref({
  scope: 'org',
  target_id: 'org-1',
  target_name: '测试企业',
  status: 'ok',
  ragflow_dataset_id: 'remote-ds-1',
  ragflow_dataset_name: 'ocm-org-test',
  embedding_model: { name: 'BAAI/bge-m3', label: 'BAAI/bge-m3', provider: 'OpenAI-API-Compatible', available: true },
  doc_num: 2,
  chunk_num: 8,
})
const models = ref({
  items: [
    { name: 'BAAI/bge-m3', label: 'BAAI/bge-m3', provider: 'OpenAI-API-Compatible', available: true },
    { name: 'netease-youdao/bce-embedding-base_v1', label: 'netease-youdao/bce-embedding-base_v1', provider: 'OpenAI-API-Compatible', available: true },
  ],
})
const mutateAsync = vi.fn()

vi.mock('@/api/hooks/useKnowledge', () => ({
  useRAGFlowDatasetInfoQuery: () => ({
    data: info,
    isLoading: ref(false),
    error: ref(null),
    refetch: vi.fn(),
  }),
  useKnowledgeEmbeddingModelsQuery: () => ({
    data: models,
    isLoading: ref(false),
    error: ref(null),
    refetch: vi.fn(),
  }),
  useUpdateRAGFlowDatasetEmbeddingModel: () => ({
    mutateAsync,
    isPending: ref(false),
  }),
}))

describe('RAGFlowDatasetInfoDialog', () => {
  beforeEach(() => {
    mutateAsync.mockReset()
    info.value = {
      scope: 'org',
      target_id: 'org-1',
      target_name: '测试企业',
      status: 'ok',
      ragflow_dataset_id: 'remote-ds-1',
      ragflow_dataset_name: 'ocm-org-test',
      embedding_model: { name: 'BAAI/bge-m3', label: 'BAAI/bge-m3', provider: 'OpenAI-API-Compatible', available: true },
      doc_num: 2,
      chunk_num: 8,
    }
  })

  it('展示 RAGFlow dataset 名称和当前 embedding 模型', async () => {
    // 弹框打开后应展示远端 dataset 信息，便于平台管理员核对 RAGFlow 侧名称。
    const wrapper = mount(RAGFlowDatasetInfoDialog, {
      props: { visible: true, scope: 'org', targetId: 'org-1', targetName: '测试企业' },
      global: { stubs: ['n-modal', 'n-spin', 'n-alert', 'n-descriptions', 'n-descriptions-item', 'n-form', 'n-form-item', 'n-select', 'n-space', 'n-button', 'ConfirmActionModal'] },
    })
    expect(wrapper.text()).toContain('ocm-org-test')
    expect(wrapper.text()).toContain('BAAI/bge-m3')
  })

  it('not_created 状态禁用保存按钮', async () => {
    // 尚未创建远端 dataset 时不能修改模型，也不能触发懒创建。
    info.value = { scope: 'org', target_id: 'org-1', target_name: '测试企业', status: 'not_created' }
    const wrapper = mount(RAGFlowDatasetInfoDialog, {
      props: { visible: true, scope: 'org', targetId: 'org-1', targetName: '测试企业' },
      global: { stubs: ['n-modal', 'n-spin', 'n-alert', 'n-descriptions', 'n-descriptions-item', 'n-form', 'n-form-item', 'n-select', 'n-space', 'n-button', 'ConfirmActionModal'] },
    })
    expect(wrapper.text()).toContain('尚未创建 RAGFlow dataset')
    expect(wrapper.text()).toContain('保存并重新解析')
  })

  it('提交时使用模型 name 和 provider 而不是内部 ID', async () => {
    // 前端只提交用户可识别的模型名，内部 RAGFlow ID 由后端解析。
    mutateAsync.mockResolvedValue(info.value)
    const wrapper = mount(RAGFlowDatasetInfoDialog, {
      props: { visible: true, scope: 'org', targetId: 'org-1', targetName: '测试企业' },
      global: { stubs: ['n-modal', 'n-spin', 'n-alert', 'n-descriptions', 'n-descriptions-item', 'n-form', 'n-form-item', 'n-select', 'n-space', 'n-button'] },
    })
    await wrapper.vm.$emit('update:selectedModelKey', 'netease-youdao/bce-embedding-base_v1|OpenAI-API-Compatible')
    await wrapper.find('form').trigger('submit')
    await wrapper.findComponent({ name: 'ConfirmActionModal' }).vm.$emit('confirm')
    expect(mutateAsync).toHaveBeenCalledWith({
      name: 'netease-youdao/bce-embedding-base_v1',
      provider: 'OpenAI-API-Compatible',
    })
  })
})
```

- [ ] **Step 7: Run frontend unit tests for shared pieces**

Run:

```bash
rtk npm --prefix web run test -- src/domain/permissions.spec.ts src/components/RAGFlowDatasetInfoDialog.spec.ts
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
rtk git add web/src/domain/permissions.ts web/src/domain/permissions.spec.ts web/src/api/hooks/useKnowledge.ts web/src/api/hooks/useIndustryKnowledge.ts web/src/components/RAGFlowDatasetInfoDialog.vue web/src/components/RAGFlowDatasetInfoDialog.spec.ts
rtk git commit -m "feat(web): 增加 RAGFlow 信息弹框"
```

Commit body:

```text
新增共享 RAGFlow dataset 信息弹框和前端 API hooks。

弹框统一展示远端 dataset 信息，修改模型时只提交模型名和 provider，并在成功后刷新对应知识库文件列表。
```

### Task 6: Wire Three Knowledge Pages

**Files:**
- Modify: `web/src/pages/platform/IndustryKnowledgePage.vue`
- Modify: `web/src/pages/platform/IndustryKnowledgePage.spec.ts`
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.vue`
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.spec.ts`
- Modify: `web/src/pages/apps/AppKnowledgeTab.vue`
- Modify: `web/src/pages/apps/AppKnowledgeTab.spec.ts`

- [ ] **Step 1: Add industry page entry**

In `IndustryKnowledgePage.vue`, import dialog and permission helper:

```ts
import RAGFlowDatasetInfoDialog from '@/components/RAGFlowDatasetInfoDialog.vue'
import { canManageRAGFlowDatasetInfo } from '@/domain/permissions'
import { useAuthStore } from '@/stores/auth'
```

Add state:

```ts
const auth = useAuthStore()
const ragflowDialogOpen = ref(false)
const ragflowDialogTarget = ref<IndustryKnowledgeBase | null>(null)
const canManageRAGFlowInfo = computed(() => canManageRAGFlowDatasetInfo(auth.user))

function openRAGFlowInfo(row: IndustryKnowledgeBase) {
  ragflowDialogTarget.value = row
  ragflowDialogOpen.value = true
}
```

Add row action:

```ts
canManageRAGFlowInfo.value
  ? h(NButton, { size: 'small', onClick: () => openRAGFlowInfo(row) }, { default: () => 'RAGFlow 信息' })
  : null
```

Add component near bottom of template:

```vue
<RAGFlowDatasetInfoDialog
  v-if="ragflowDialogTarget"
  v-model:visible="ragflowDialogOpen"
  scope="industry"
  :target-id="ragflowDialogTarget.id"
  :target-name="ragflowDialogTarget.name"
/>
```

- [ ] **Step 2: Add org page entry**

In `OrgKnowledgePage.vue`, add a header button inside `#header-extra` before upload controls:

```vue
<n-button
  v-if="canManageRAGFlowInfo && effectiveOrgId"
  size="small"
  @click="ragflowDialogOpen = true"
>
  RAGFlow 信息
</n-button>
```

Add state:

```ts
const ragflowDialogOpen = ref(false)
const canManageRAGFlowInfo = computed(() => canManageRAGFlowDatasetInfo(auth.user))
```

Add dialog:

```vue
<RAGFlowDatasetInfoDialog
  v-if="effectiveOrgId"
  v-model:visible="ragflowDialogOpen"
  scope="org"
  :target-id="effectiveOrgId"
  target-name="企业知识库"
/>
```

- [ ] **Step 3: Add app page entry**

In `AppKnowledgeTab.vue`, add button in header extra:

```vue
<n-button v-if="canManageRAGFlowInfo" size="small" @click="ragflowDialogOpen = true">
  RAGFlow 信息
</n-button>
```

Add state:

```ts
const ragflowDialogOpen = ref(false)
const canManageRAGFlowInfo = computed(() => canManageRAGFlowDatasetInfo(auth.user))
```

Add dialog:

```vue
<RAGFlowDatasetInfoDialog
  v-model:visible="ragflowDialogOpen"
  scope="app"
  :target-id="props.appId"
  :target-name="app?.value?.name || '实例知识库'"
/>
```

- [ ] **Step 4: Add page tests**

Add or extend tests:

```ts
it('platform_admin 可以看到 RAGFlow 信息入口', () => {
  // 平台管理员需要通过入口查看远端 dataset 名称并调整 embedding 模型。
})

it('org_admin 看不到 RAGFlow 信息入口', () => {
  // 企业管理员仍可管理文件或容量，但不能触发 RAGFlow dataset 运维弹框。
})
```

For industry page, also assert clicking row action opens `RAGFlowDatasetInfoDialog` with `scope="industry"` and selected row id.

- [ ] **Step 5: Run page tests**

Run:

```bash
rtk npm --prefix web run test -- src/pages/platform/IndustryKnowledgePage.spec.ts src/pages/knowledge/OrgKnowledgePage.spec.ts src/pages/apps/AppKnowledgeTab.spec.ts
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
rtk git add web/src/pages/platform/IndustryKnowledgePage.vue web/src/pages/platform/IndustryKnowledgePage.spec.ts web/src/pages/knowledge/OrgKnowledgePage.vue web/src/pages/knowledge/OrgKnowledgePage.spec.ts web/src/pages/apps/AppKnowledgeTab.vue web/src/pages/apps/AppKnowledgeTab.spec.ts
rtk git commit -m "feat(web): 接入三类知识库 RAGFlow 信息入口"
```

Commit body:

```text
行业知识库、企业知识库和实例知识库统一接入 RAGFlow 信息弹框。

入口仅平台管理员可见，弹框共用同一组件和模型修改流程。
```

### Task 7: Full Verification

**Files:**
- No planned source edits unless verification exposes defects.

- [ ] **Step 1: Run backend targeted tests**

Run:

```bash
rtk go test ./internal/config ./internal/integrations/ragflow ./internal/service ./internal/api/handlers -count=1
```

Expected: PASS.

- [ ] **Step 2: Run frontend tests and typecheck**

Run:

```bash
rtk npm --prefix web run test -- src/domain/permissions.spec.ts src/components/RAGFlowDatasetInfoDialog.spec.ts src/pages/platform/IndustryKnowledgePage.spec.ts src/pages/knowledge/OrgKnowledgePage.spec.ts src/pages/apps/AppKnowledgeTab.spec.ts
rtk npm --prefix web run typecheck
```

Expected: PASS.

- [ ] **Step 3: Verify generated artifacts are synced**

Run:

```bash
rtk make openapi-check
```

Expected: command exits 0 and reports no generated OpenAPI drift.

- [ ] **Step 4: Browser verification**

Use the local k3d manager at `http://ocm.localhost` with local platform admin `admin` / `admin123`.

Verify in a real browser:
- Industry knowledge page shows `RAGFlow 信息` in row actions for platform admin.
- Org knowledge page shows `RAGFlow 信息` for platform admin.
- App knowledge tab shows `RAGFlow 信息` for platform admin.
- Dialog displays RAGFlow dataset ID/name and current embedding model.
- Selecting `BAAI/bge-m3` or `netease-youdao/bce-embedding-base_v1` submits the human model name and refreshes file list state to queued/running.
- Login as an org admin or org member and verify the entry is absent.

- [ ] **Step 5: Inspect git diff**

Run:

```bash
rtk git status --porcelain=v1
rtk git diff --stat
```

Expected: only files listed in this plan are modified.

- [ ] **Step 6: Handle verification fixes**

If verification exposes defects, return to the task that introduced the defect, edit the concrete source files named by the failing test or browser check, rerun that task's verification command, and amend that task's commit with:

```bash
rtk git commit --amend
```

Expected: no empty commit is created; each functional commit remains scoped to one business change.

## Self-Review

- Spec coverage: plan covers platform-admin-only visibility and operations, three knowledge scopes, realtime RAGFlow dataset info, no local model persistence, config default model on creation, fallback model list, model update through RAGFlow, full reparse trigger, local document status reset, shared dialog, OpenAPI/type generation, tests, and browser verification.
- Path adjustment: implementation uses existing project route prefix `/api/v1/organizations/:orgId/knowledge/...` for enterprise knowledge instead of the design draft shorthand `/api/v1/orgs/...`.
- Internal model ID handling: config and frontend only use `name/provider`; RAGFlow internal IDs remain backend-only and are not required from operators.
- Placeholder scan: no implementation step relies on an unspecified file, unspecified command, or deferred error handling instruction.
- Type consistency: backend service input/result names are `KnowledgeEmbeddingModelInput`, `KnowledgeEmbeddingModelResult`, `KnowledgeEmbeddingModelListResult`, and `KnowledgeRAGFlowDatasetInfoResult`; frontend names mirror those concepts as `KnowledgeEmbeddingModel`, `KnowledgeEmbeddingModelList`, and `KnowledgeRAGFlowDatasetInfo`.
