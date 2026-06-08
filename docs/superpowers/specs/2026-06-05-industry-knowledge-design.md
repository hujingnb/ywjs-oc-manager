# 设计文档：行业知识库

**日期：** 2026-06-05
**状态：** 待用户复核

## 背景

当前知识库已经迁移为 RAGFlow 主库，manager 只维护企业 / 实例与 RAGFlow dataset、document 的映射：

- 企业知识库（`org`）：企业内共享资料；
- 实例知识库（`app`）：单个 Hermes 实例私有资料；
- Hermes 通过 `oc-kb search` 调 manager runtime API，manager 固定检索当前实例库和所属企业库。

新需求是在这两层之外增加平台级“行业知识库”。商业知识库外部服务会把行业资料上传到 manager，平台管理员可以管理行业库；助手版本可以选择一个或多个行业库，绑定该版本的实例在检索时除了实例库和企业库，还要额外检索这些行业库。

## 目标

- 行业知识库是平台级全局资源，不归属任何企业。
- 行业名称全局唯一；不引入行业编码。
- 外部商业知识库上传接口按行业名称定位行业库，不存在时自动创建。
- 外部上传接口使用配置文件中的固定鉴权字符串保护，不复用平台管理员 JWT。
- 外部上传接口使用 `multipart/form-data`，包含 `industry_name` 和 `file`。
- 同一行业库内同名文件使用覆盖语义：先删除旧 document，再上传新 document 并触发解析。
- 平台管理员可以完整管理行业库和行业库文件。
- 助手版本可以选择 0..N 个行业库。
- 行业库选择变更立即影响 runtime search，不需要重启实例，也不递增助手版本 `revision`。
- runtime search 结果顺序固定为：实例库 → 企业库 → 每个关联行业库。
- 每个关联行业库都独立返回最多 `top_k` 条结果，由平台管理员控制选择数量以避免上下文膨胀。
- 行业库不设置累计容量上限，仅保留单文件上传硬上限。

## 非目标

- 不做企业级行业库授权；所有行业库对平台的助手版本目录全局可选。
- 不做行业编码、行业层级、行业标签或行业分类树。
- 不把 RAGFlow API key 暴露给外部商业知识库服务、前端或 Hermes。
- 不让外部上传 token 获得平台管理员权限。
- 不做行业库检索总量压缩；不把多个行业库共享一个行业总 `top_k`。
- 不新增 `oc-kb` CLI 参数；Hermes 不需要知道助手版本选择了哪些行业库。
- 不为行业库设置累计配额。

## 推荐方案

采用“新增平台级行业库资源 + 扩展 RAGFlow scope”的方案。

manager 新增行业库主表，并把现有 `ragflow_datasets` / `ragflow_documents` 扩展为支持 `scope_type='industry'`。行业库文件生命周期继续复用当前 RAGFlow-backed 知识库能力：创建 dataset、上传 document、触发解析、下载、删除、重解析、后台刷新解析状态。

这个方案的核心取舍是：行业库成为 manager 内的一等平台资源，但文件主库、解析状态和检索索引仍由 RAGFlow 承担。相比单独复制一套行业库 document 表，它能复用现有 KnowledgeService、RAGFlow client 和后台状态刷新机制。

## 数据模型

### `industry_knowledge_bases`

新增平台级行业库主表：

- `id`：行业库 ID。
- `name`：行业名称，在未删除行业库中全局唯一。软删除后同名可以重新创建。
- `created_by`：创建来源。平台管理员创建时记录用户 ID；外部上传自动创建时记录固定来源，例如 `external:industry-knowledge`。
- `created_at` / `updated_at` / `deleted_at`：生命周期时间。

行业库删除使用软删除，便于审计和避免误删后 ID 复用带来的历史关联歧义。

### `ragflow_datasets`

扩展现有 dataset 映射表：

- 新增 `industry_knowledge_base_id CHAR(36) NULL`。
- `scope_type` 允许 `org`、`app`、`industry`。
- `org` scope：`org_id` 必填，`app_id` 和 `industry_knowledge_base_id` 为空。
- `app` scope：`org_id` 和 `app_id` 必填，`industry_knowledge_base_id` 为空。
- `industry` scope：`industry_knowledge_base_id` 必填，`org_id` 和 `app_id` 为空。
- 新增行业库唯一映射约束，保证每个行业库最多一个 RAGFlow dataset。

实现时需要调整现有外键和 generated unique key，使 org/app 约束保持原语义，同时 industry scope 不需要伪造组织 ID。

### `ragflow_documents`

扩展现有 document 元数据缓存表：

- 新增 `industry_knowledge_base_id CHAR(36) NULL`。
- `scope_type` 允许 `org`、`app`、`industry`。
- `industry` document 关联行业库 dataset，并冗余保存行业库 ID，方便列表、覆盖、删除和检索来源回填。
- 行业库内同名文件以 manager 归一化后的 `path.Base(filename)` 判断。

为了支撑覆盖语义，行业库文件名在同一行业库内保持唯一。迁移新增行业 scope 专用唯一约束，约束键为 `industry_knowledge_base_id` 和归一化后的 `name`；若发生并发同名覆盖，服务层以最后成功写入的 document 作为有效文件，并清理失败分支已经上传但未能落库的远端 document。

### `assistant_version_industry_knowledge_bases`

新增助手版本与行业库的关联表：

- `version_id`：助手版本 ID。
- `industry_knowledge_base_id`：行业库 ID。
- `created_at`：关联创建时间。
- 主键：`(version_id, industry_knowledge_base_id)`。

删除行业库前必须检查是否被任何未删除助手版本引用；有引用返回 409，不自动清理关联。

## API 与权限

### 平台管理接口

仅 `platform_admin` 可调用：

- `GET /api/v1/industry-knowledge-bases`：行业库列表，返回 ID、名称、文件数、更新时间等摘要。
- `POST /api/v1/industry-knowledge-bases`：创建行业库，名称必填且全局唯一。
- `PUT /api/v1/industry-knowledge-bases/{id}`：重命名行业库，名称仍全局唯一。
- `DELETE /api/v1/industry-knowledge-bases/{id}`：删除行业库；若被未删除助手版本引用返回 409。
- `GET /api/v1/industry-knowledge-bases/{id}/knowledge`：行业库文件列表。
- `POST /api/v1/industry-knowledge-bases/{id}/knowledge`：平台管理员手动上传文件，同名覆盖。
- `GET /api/v1/industry-knowledge-bases/{id}/knowledge/{documentId}/file`：下载文件。
- `DELETE /api/v1/industry-knowledge-bases/{id}/knowledge/{documentId}`：删除文件。
- `POST /api/v1/industry-knowledge-bases/{id}/knowledge/{documentId}/reparse`：重新解析文件。

权限谓词继续集中在 `internal/auth/authorizer.go`，新增 `CanManageIndustryKnowledge`，避免 handler/service 内联角色判断。

### 外部上传接口

外部商业知识库服务调用：

- `POST /api/v1/external/industry-knowledge/files`
- 请求格式：`multipart/form-data`
- 字段：
  - `industry_name`：行业名称，必填。
  - `file`：上传文件，必填。
- 鉴权：请求头 `X-OC-Industry-Knowledge-Token` 必须等于配置文件中的固定字符串。

配置项：

```yaml
industry_knowledge:
  upload_token: "CHANGE_ME_INDUSTRY_KNOWLEDGE_UPLOAD_TOKEN"
```

外部 token 只授予“上传行业库文件、按名称自动创建行业库”的能力，不等同用户登录态，也不能访问平台管理接口。

外部上传行为：

1. 校验固定 token。
2. 校验 `industry_name` 和 `file`。
3. 按行业名称查找未删除行业库，不存在则自动创建。
4. 确保该行业库的 RAGFlow dataset 可用。
5. 按归一化文件名查找同名 document。
6. 若同名 document 存在，先删除旧 RAGFlow document 和本地映射。
7. 上传新 document，写入本地映射并触发解析。
8. 返回 `202 Accepted` 和新 document 元数据。

若旧 document 删除失败，不继续上传，避免同名文件出现两份。若旧文件已删除但新文件上传失败，接口返回失败原因；这是覆盖语义下可接受的失败结果。

## 助手版本

助手版本创建/更新请求新增 `industry_knowledge_base_ids`，默认为空数组。版本详情和列表返回 `industry_knowledge_bases`，每项至少包含 `id` 和 `name`。

规则：

- 只有平台管理员可编辑助手版本的行业库关联。
- 提交的行业库 ID 必须存在且未删除。
- 重复 ID 归一化去重。
- 行业库关联变化不递增助手版本 `revision`。
- 如果同一次更新同时修改了 system prompt、image、model、routing 等容器相关字段，`revision` 仍按现有规则递增；行业库关联本身不参与 revision 判断。
- 行业库选择立即生效，已绑定该版本的实例无需重启。

## Runtime 检索

`oc-kb` CLI 保持不变，仍只提交 `question` 和 `top_k`。manager runtime search 负责根据 app 当前绑定版本扩展检索范围。

流程：

1. 通过 `X-OC-App-Token` 解析当前 app。
2. 检索当前实例 RAGFlow dataset，最多返回 `top_k` 条，结果 `scope='app'`。
3. 检索所属企业 RAGFlow dataset，最多返回 `top_k` 条，结果 `scope='org'`。
4. 读取 app 绑定助手版本关联的行业库。
5. 对每个行业库单独检索其 RAGFlow dataset，单个行业库最多返回 `top_k` 条，结果 `scope='industry'`。
6. 最终结果按 `app → org → industry` 追加；多个行业库按行业名称升序、ID 升序追加，保证结果顺序稳定。

如果版本关联了 5 个行业库且 `top_k=8`，runtime search 最多返回：

- 实例库：8 条；
- 企业库：8 条；
- 行业库：5 * 8 条。

manager 不对行业库结果做总量压缩。上下文长度风险由平台管理员在助手版本配置时控制。

`KnowledgeSearchHit` 扩展字段：

- `scope`：`app` / `org` / `industry`。
- `document_id`
- `document_name`
- `content`
- `similarity`
- `industry_knowledge_base_id`：仅行业库命中返回。
- `industry_knowledge_base_name`：仅行业库命中返回。

Hermes 可以通过来源字段识别行业库命中，但不需要知道或提交行业库 ID。

## 文件生命周期

### 上传与覆盖

行业库上传不做累计容量校验，但保留现有单文件大小硬上限。文件名使用 `path.Base(filename)` 归一化，避免外部服务通过路径片段写入异常名称。

覆盖只在同一个行业库内生效，不影响其它行业库中的同名文件。

### 删除行业库

删除流程：

1. 检查是否被任何未删除助手版本引用；有引用返回 409。
2. 查找行业库对应 RAGFlow dataset。
3. 调 RAGFlow 删除远端 dataset。
4. 删除本地 dataset/document 映射。
5. 软删除行业库主表记录。

若 RAGFlow 删除失败，不删除本地记录，保留后续重试和排障入口。

### 解析状态

行业库 document 写入同一张 `ragflow_documents` 表，因此现有 `ragflow_parse_status_refresh` 后台任务继续扫描 `queued` / `running` 状态即可。状态刷新时需要支持 `industry` scope 的 dataset 和 document 映射。

## 前端

### 行业知识库页面

新增平台菜单“行业知识库”，仅 `platform_admin` 可见。

页面功能：

- 行业库列表。
- 创建行业库。
- 重命名行业库。
- 删除行业库。
- 选中行业库后展示扁平文件列表。
- 上传文件；同名文件会覆盖，页面显示明确提示。
- 下载、删除、重解析文件。
- 展示解析状态、进度、错误、文件大小、上传时间。

行业库页面不显示累计容量和剩余容量，只显示单文件上传上限提示。

### 助手版本页面

助手版本编辑页新增“行业知识库”多选控件：

- 支持搜索行业库名称。
- 显示已选择数量。
- 保存后立即生效。
- 不显示“需重启”提示。
- 固定提示文案：

```text
每选一个行业知识库，系统都会多查一批参考内容。选得越多，回答要处理的内容越多，速度和费用都可能增加。建议只选当前版本真正需要的行业库。
```

## 错误处理

错误码：

- `INDUSTRY_KNOWLEDGE_TOKEN_INVALID`：外部上传 token 缺失或错误，返回 401。
- `INDUSTRY_KNOWLEDGE_NOT_FOUND`：行业库不存在或已删除，返回 404。
- `INDUSTRY_KNOWLEDGE_NAME_TAKEN`：行业名称冲突，返回 409。
- `INDUSTRY_KNOWLEDGE_IN_USE`：行业库被助手版本引用，删除返回 409。
- `KNOWLEDGE_FORBIDDEN`：用户侧权限不足，返回 403。
- `KNOWLEDGE_QUOTA_EXCEEDED` 不用于行业库累计容量，因为行业库没有累计配额。

RAGFlow 创建 dataset、上传、删除、解析失败时，沿用现有知识库错误映射，返回可读失败原因。

## 测试

### 后端

需要覆盖：

- 外部上传 token 缺失或错误返回 401。
- 外部上传按行业名称自动创建行业库。
- 外部上传同名文件覆盖旧 document。
- 平台管理员创建、重命名、删除行业库。
- 非平台管理员不能调用平台行业库管理接口。
- 行业名称唯一冲突返回 409。
- 被未删除助手版本引用的行业库不可删除。
- 助手版本保存行业库关联后不递增 `revision`。
- runtime search 按 `app → org → 每个行业库` 顺序调用 RAGFlow retrieval。
- 每个行业库各自返回 `top_k`，不共享行业总 `top_k`。
- 行业库 retrieval hit 返回行业库 ID 和名称。
- 行业库 document 解析状态能被现有刷新任务回写。

### 前端

需要覆盖：

- 行业知识库页面创建、重命名、删除行业库。
- 行业知识库页面上传同名文件时展示覆盖提示。
- 行业知识库页面下载、删除、重解析文件。
- 助手版本页选择多个行业库。
- 助手版本页展示上下文膨胀提示。
- 保存行业库选择后不触发“需重启”相关 UI。

### 生成与验证

修改 handler、DTO、路由或响应结构后必须运行：

```bash
make openapi-gen
make web-types-gen
```

提交前还需要运行相关 Go 单测和前端单测。作为新平台功能，完成实现后需要用真实浏览器验证：

- 平台管理员可以创建和管理行业知识库；
- 助手版本可以选择多个行业库并看到提示；
- 选择行业库后保存立即生效；
- runtime search 能返回行业库命中并带来源字段。

外部上传接口可以用 API 请求验证鉴权、自动创建和覆盖行为；浏览器验证重点放在平台管理和助手版本配置流程。

## 交付影响

- 需要新增数据库迁移并重新生成 sqlc。
- 需要新增配置项和配置校验。
- 需要新增 OpenAPI 注解并重新生成前端类型。
- 需要更新 `docs/knowledge-base.md` 和 `docs/user-manual.md`，说明三层知识库和行业库检索行为。
- 需要更新 Hermes runtime 的 `oc-kb` skill / `SOUL.md` 指引文案，让模型知道检索结果可能包含行业知识库来源，但不需要修改 CLI 参数。
