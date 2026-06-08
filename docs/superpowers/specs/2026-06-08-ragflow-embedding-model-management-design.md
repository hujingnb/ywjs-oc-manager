# 设计文档：RAGFlow 知识库 Embedding 模型管理

## 背景

manager 目前通过 RAGFlow 管理企业知识库、实例知识库和行业知识库。创建 RAGFlow dataset 时，
manager 只传 `name` 和 `chunk_method`，embedding 模型由 RAGFlow 当前 tenant 默认值决定。
这会导致线上默认模型调整后，历史 dataset 不会自动变更，新建 dataset 也容易受到 RAGFlow
tenant 默认值、模型注册状态和创建时间影响。

本次设计让平台管理员可以在 manager 后台查看指定知识库对应的 RAGFlow dataset 信息，并修改该
dataset 的 embedding 模型。修改成功后，manager 触发该知识库下全部文件重新进入解析流程。

## 目标

- 覆盖三类知识库：行业知识库、企业知识库、实例知识库。
- 仅 `platform_admin` 可以看到 RAGFlow 信息入口并执行模型修改。
- manager 后台可以实时查看 RAGFlow dataset 名称、远端 ID 和当前 embedding 模型。
- 新建 RAGFlow dataset 时显式使用 manager 配置中的默认 embedding 模型，不再依赖 RAGFlow tenant 默认值。
- 修改已有 dataset embedding 模型时调用 RAGFlow 接口，并全量触发重新解析。
- 修改后 manager 本地所有该知识库文件状态回到正常解析起点，由现有状态刷新任务继续回写。
- 模型列表优先从 RAGFlow 接口获取；获取不到时使用 manager 配置文件兜底。

## 非目标

- 不管理 parser/chunk method、chat model、rerank model、OCR model 等非 embedding 模型。
- 不在 manager 数据库缓存 RAGFlow embedding 模型或 dataset 详情，避免双边数据不一致。
- 不把 RAGFlow 模型 API key、base URL 等供应商凭据写入 manager 配置或仓库；这些仍在 RAGFlow
  侧模型配置中维护。
- 不改变企业用户、普通成员对知识库文件上传、删除、重新解析的现有权限。

## 现状依据

- 行业知识库、企业知识库和实例知识库都已通过 `ragflow_datasets` 维护本地 dataset 映射。
- RAGFlow v0.25.6 提供：
  - `POST /api/v1/datasets` 创建 dataset，支持 `embedding_model`。
  - `GET /api/v1/datasets/{dataset_id}` 查询 dataset。
  - `PUT /api/v1/datasets/{dataset_id}` 更新 dataset，支持修改 embedding 模型。
  - `POST /api/v1/datasets/{dataset_id}/embedding` 触发全库 embedding 重跑。
  - 控制台侧 `/my_llms?include_details=true` 与 `/list?model_type=embedding` 可作为模型列表来源。
- RAGFlow 内部可能把 OpenAI compatible 模型名扩展为 `xxx___OpenAI-API@OpenAI-API-Compatible`
  形式。该内部值不适合直接暴露给 manager 配置或前端用户。

## 配置设计

manager 配置新增：

```yaml
ragflow:
  default_embedding_model: "BAAI/bge-m3"
  embedding_models:
    - name: "BAAI/bge-m3"
      label: "BAAI/bge-m3"
      provider: "OpenAI-API-Compatible"
    - name: "netease-youdao/bce-embedding-base_v1"
      label: "netease-youdao/bce-embedding-base_v1"
      provider: "OpenAI-API-Compatible"
```

字段语义：

- `default_embedding_model` 是新建 dataset 的默认 embedding 模型，填写人可识别的模型名。
- `embedding_models[].name` 是在 RAGFlow 创建模型时看到的模型名。
- `embedding_models[].provider` 用于和 RAGFlow 返回的 factory/provider 匹配。
- `embedding_models[].label` 是前端展示名；为空时回退到 `name`。

后端负责把 `name + provider` 解析成 RAGFlow 实际需要的内部模型标识。配置文件不要求填写
`___OpenAI-API@OpenAI-API-Compatible` 这类内部值。

## 后端设计

### RAGFlow 客户端

扩展 `internal/integrations/ragflow`：

- `CreateDataset(ctx, req)`：创建时支持 `EmbeddingModel`，请求体使用 RAGFlow 接口字段
  `embedding_model`。
- `GetDataset(ctx, datasetID)`：读取远端 dataset 信息，返回 `id/name/embd_id/tenant_embd_id/parser_id/doc_num/chunk_num`。
- `UpdateDatasetEmbeddingModel(ctx, datasetID, embeddingModel)`：调用 `PUT /api/v1/datasets/{dataset_id}`。
- `RunDatasetEmbedding(ctx, datasetID)`：调用 `POST /api/v1/datasets/{dataset_id}/embedding`。
- `ListEmbeddingModels(ctx)`：优先尝试 RAGFlow 模型列表接口，归一化为 `name/provider/label/internal_id/available`。

RAGFlow 模型标识解析规则：

- 前端和配置只使用 `name/provider`。
- 后端从 RAGFlow 模型列表中寻找同名同 provider 的可用 embedding 模型。
- 找到时使用 RAGFlow 返回或可推导出的内部提交值。
- 找不到时，如果这是配置兜底项，可以展示给前端；真正提交时仍由 RAGFlow 更新接口最终校验。

### Service 能力

新增统一的 dataset 信息和模型修改能力，内部按 scope 分发：

- `GetKnowledgeRAGFlowDatasetInfo(ctx, principal, scope, targetID)`
- `UpdateKnowledgeEmbeddingModel(ctx, principal, scope, targetID, modelName, provider)`
- `ListKnowledgeEmbeddingModels(ctx, principal)`

权限：

- 三个方法都只允许 `platform_admin`。
- 权限判断新增或复用集中在 `internal/auth/authorizer.go`，service 内不内联角色判断。

读取信息：

- 根据 scope 定位本地 `ragflow_datasets`：
  - `industry` 通过行业知识库 ID。
  - `org` 通过组织 ID。
  - `app` 通过实例 ID。
- 如果本地没有映射或远端 dataset ID 为空，返回 `status=not_created`，不做懒创建。
- 如果有远端 ID，调用 RAGFlow detail 接口实时读取。
- RAGFlow detail 读取失败时，读取接口仍返回 200 和 `status=error`，并带 `error_message` 供弹框展示；
  权限不足、manager 本地目标不存在等 manager 自身错误仍按现有 HTTP 错误处理。

修改模型：

1. 校验 `platform_admin`。
2. 定位本地 dataset 映射并要求远端 ID 已存在。
3. 校验目标模型存在于 RAGFlow 实时模型列表或配置兜底列表。
4. 调用 RAGFlow 更新 dataset embedding 模型。
5. 调用 RAGFlow 全库 embedding 重跑接口。
6. 将 manager 本地该 dataset 下全部 `ragflow_documents` 状态重置为：
   - `parse_status='queued'`
   - `progress=0`
   - `last_error=NULL`
7. 返回更新后的 RAGFlow dataset 信息。

失败语义：

- 更新模型失败：不重置本地文件状态，直接返回失败。
- 模型更新成功但触发重解析失败：返回失败，不重置本地状态，提示模型可能已更新但重解析未触发。
- RAGFlow 接受重解析后，本地状态重置失败：返回失败，并记录错误日志；远端可能已开始重解析，需要人工重试或补偿。

### API 契约

新增模型列表接口：

- `GET /api/v1/knowledge/embedding-models`

新增三类 dataset 信息接口：

- `GET /api/v1/orgs/{orgId}/knowledge/ragflow-dataset`
- `PATCH /api/v1/orgs/{orgId}/knowledge/ragflow-dataset/embedding-model`
- `GET /api/v1/apps/{appId}/knowledge/ragflow-dataset`
- `PATCH /api/v1/apps/{appId}/knowledge/ragflow-dataset/embedding-model`
- `GET /api/v1/industry-knowledge-bases/{industryId}/ragflow-dataset`
- `PATCH /api/v1/industry-knowledge-bases/{industryId}/ragflow-dataset/embedding-model`

响应结构：

```json
{
  "scope": "industry",
  "target_id": "industry-id",
  "target_name": "金融证券",
  "status": "ok",
  "ragflow_dataset_id": "remote-id",
  "ragflow_dataset_name": "ocm-industry-...",
  "embedding_model": {
    "name": "BAAI/bge-m3",
    "provider": "OpenAI-API-Compatible",
    "label": "BAAI/bge-m3"
  },
  "error_message": "",
  "doc_num": 12,
  "chunk_num": 345,
  "updated_at": "2026-06-08T12:00:00Z"
}
```

`status` 取值：

- `ok`：已创建远端 dataset 且 RAGFlow 信息读取成功。
- `not_created`：本地尚无远端 dataset 映射。
- `error`：RAGFlow 信息读取失败；此时 `error_message` 给平台管理员排障提示，修改模型按钮禁用。

修改请求：

```json
{
  "name": "BAAI/bge-m3",
  "provider": "OpenAI-API-Compatible"
}
```

## 前端设计

三处知识库入口统一：

- 行业知识库页面：在行业库行操作或选中行业库操作区增加 `RAGFlow 信息` 按钮。
- 企业知识库页面：在页面顶部工具栏增加 `RAGFlow 信息` 按钮。
- 实例知识库页面：在知识库 tab 顶部工具栏增加 `RAGFlow 信息` 按钮。
- 入口仅 `platform_admin` 可见。

共用组件：

- 新增 `RAGFlowDatasetInfoDialog`。
- 三处页面传入 `scope`、`targetId` 和展示名。
- 弹框打开时实时请求 dataset 信息和模型列表。

弹框展示：

- manager 知识库类型和名称。
- RAGFlow dataset ID。
- RAGFlow dataset 名称。
- 当前 embedding 模型。
- 文档数、chunk 数；RAGFlow 没有返回时隐藏。
- RAGFlow 信息获取失败时显示错误和重试按钮。

修改流程：

1. 平台管理员点击 `RAGFlow 信息`。
2. 弹框加载 RAGFlow 实时信息和模型列表。
3. 选择目标 embedding 模型。
4. 二次确认：会更新 RAGFlow dataset embedding 模型，并使该知识库下全部文件重新进入解析流程。
5. 成功后刷新弹框信息和文件列表，文件状态展示为排队或解析中。

## 测试设计

后端测试：

- RAGFlow client 创建 dataset 时携带 `embedding_model`。
- RAGFlow client 能解析 dataset detail 的 `embd_id/name/doc_num/chunk_num`。
- RAGFlow client 更新 embedding 模型请求体正确。
- RAGFlow client 能调用全库 embedding 重跑接口。
- 模型列表优先使用 RAGFlow 接口，失败时回退配置。
- service 只允许 `platform_admin` 获取 RAGFlow 信息和修改模型。
- dataset 未创建时返回 `not_created`，不触发懒创建。
- 模型修改成功后，全部本地文件状态重置为 queued。
- 模型更新失败、重解析触发失败时，不重置本地文件状态。

前端测试：

- 行业知识库、企业知识库、实例知识库仅平台管理员显示 `RAGFlow 信息` 按钮。
- 三处页面都复用同一个弹框组件，并传入正确 scope 和 target ID。
- 弹框覆盖加载、失败、重试、模型下拉、二次确认、成功刷新。
- 非平台管理员不触发 RAGFlow 信息请求。

生成与验证：

- 修改 handler/DTO 后运行 `make openapi-gen` 和 `make web-types-gen`。
- 运行相关 Go 单元测试与前端组件测试。
- 完成功能后用真实浏览器验证三处页面：
  - 平台管理员可打开弹框并看到 RAGFlow 信息。
  - 模型修改后文件状态重新进入解析流程。
  - 非平台管理员看不到入口。

## 风险与约束

- RAGFlow 模型列表接口属于控制台侧接口，若 API key 不支持访问，manager 必须回退配置列表。
- RAGFlow 内部模型标识和展示名不同，manager 后端必须负责转换，不能把内部值泄漏到配置和前端。
- 全库重解析会给 RAGFlow 和 embedding 服务带来负载；前端二次确认必须明确提示影响。
- 不本地缓存 RAGFlow 信息意味着弹框打开依赖 RAGFlow 可用性，这是刻意取舍；主知识库文件列表仍保持可用。
