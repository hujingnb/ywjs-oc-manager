# RAGFlow 模型过载失败自动重解析设计

## 背景

线上行业知识库出现大量 RAGFlow 解析失败，页面展示的失败原因包含：

```text
[ERROR][Exception]: Error code: 503 - {'code': 50505, 'message': 'Model service overloaded. Please try again later.', 'data': None}
```

该错误来自 RAGFlow 在解析文档时调用 embedding 模型服务失败。manager 当前只把 RAGFlow 的
`progress_msg` 同步到本地 `ragflow_documents.last_error`，不会对失败文件自动重试，导致大量
文件需要人工逐个点击“重解析”。

## 目标

在 manager 后台任务中自动识别模型服务过载类临时失败，并重新触发 RAGFlow 文档解析，减少人工处理。

第一版只覆盖明确的临时上游过载错误，不泛化为所有失败自动重试。

## 非目标

- 不自动重试文件格式错误、文档内容解析错误、embedding 维度不匹配、权限或配置错误。
- 不在页面新增批量重试按钮。
- 不新增“每轮 tick 只重试 10 个”之类的低水位限流；RAGFlow 已有解析队列，生产也通过
  `MAX_CONCURRENT_TASKS` 控制实际并发。
- 不改变用户手动“重解析”的入口和权限。

## 推荐方案

在现有 `ragflow_parse_status_refresh` 后台任务中增加自动重试阶段：

1. 现有状态同步阶段继续扫描本地 `queued/running` 文档，并从 RAGFlow 拉取远端状态。
2. 当远端状态为失败时，提取 `progress_msg` 的错误文本。
3. 如果错误文本命中模型过载白名单，则把本地文档标记为 `failed`，同时写入下一次允许自动重试的时间。
4. 同一轮 tick 的自动重试阶段查询已到期的可重试失败文档，调用 RAGFlow `ParseDocuments` 重新入队。
5. 重新入队成功后，本地状态改回 `queued`，清空 `last_error`，并累计自动重试次数。

这样可以复用现有后台任务和 RAGFlow 客户端，不需要额外定时器，也不会把逻辑放到运维脚本里。

## 错误匹配规则

仅当错误文本包含以下任一片段时，认为是可自动重试的临时模型过载错误：

- `Model service overloaded`
- `Error code: 503`
- `code: 50505`

匹配大小写不敏感。其它失败原因保持 `failed`，由页面继续展示真实错误。

## 重试次数与冷却

每个文档最多自动重试 3 次。

`auto_reparse_next_at` 的作用是记录“该失败文档下一次最早允许自动重试的时间”，避免模型服务仍处于
过载状态时，后台任务每 30 秒反复提交，把 3 次自动重试在几分钟内全部耗尽。

冷却规则：

- 第 1 次自动重试：首次识别到过载失败后立即允许重试。
- 第 2 次自动重试：第 1 次自动重试后仍失败时，至少等待 10 分钟。
- 第 3 次自动重试：第 2 次自动重试后仍失败时，至少等待 30 分钟。
- 第 3 次自动重试后如果仍失败，不再自动重试，保留 `failed` 和最后一次错误，等待人工处理。

RAGFlow 队列解决的是“任务执行并发”问题；`auto_reparse_next_at` 解决的是“上游仍未恢复时不要过快消耗
自动重试机会”问题，两者作用不同。

## 数据库变更

给 `ragflow_documents` 增加两个字段：

- `auto_reparse_attempts INT NOT NULL DEFAULT 0`
  记录该文档已成功提交自动重解析的次数。
- `auto_reparse_next_at DATETIME(6) NULL`
  记录失败文档下一次可自动重试的最早时间；为空表示当前没有待冷却的自动重试。

新增索引用于查找到期的失败文档：

```sql
KEY idx_ragflow_documents_auto_reparse (
    parse_status,
    auto_reparse_next_at,
    auto_reparse_attempts,
    updated_at
)
```

新增迁移文件：

- `internal/migrations/000008_ragflow_auto_reparse.up.sql`
- `internal/migrations/000008_ragflow_auto_reparse.down.sql`

同时更新 `sqlc.yaml` 纳入新迁移，并运行 `sqlc generate`。

## 存量失败处理

上线前已经处于 `failed` 状态的文件不会再经过“远端失败状态同步”这一步；如果只新增字段而不回填，
这些存量文件的 `auto_reparse_next_at` 会保持 NULL，自动重试任务不会选中它们。

迁移需要在新增字段后执行一次存量回填：

```sql
UPDATE ragflow_documents
SET auto_reparse_next_at = NOW(6)
WHERE parse_status = 'failed'
  AND auto_reparse_attempts = 0
  AND last_error IS NOT NULL
  AND (
      LOWER(last_error) LIKE '%model service overloaded%'
      OR LOWER(last_error) LIKE '%error code: 503%'
      OR LOWER(last_error) LIKE '%code: 50505%'
  );
```

回填只设置“立即可自动重试”，不直接调用 RAGFlow。部署后由 `ragflow_parse_status_refresh` 在下一轮 tick
统一触发 `ParseDocuments`，并按正常逻辑累计 `auto_reparse_attempts`。

不命中白名单的存量失败继续保持 `failed`，避免把文件格式、维度不匹配或配置类错误自动重试。

## 查询与写入

保留现有 `ListRAGFlowDocumentsNeedingRefresh` 查询，不把普通状态同步和自动重试候选混在同一个查询里。

新增查询：

- `ListRAGFlowDocumentsDueForAutoReparse`
  查询 `parse_status='failed'`、`auto_reparse_attempts < 3`、`auto_reparse_next_at <= NOW(6)` 且远端
  dataset 已存在的文档。
- `MarkRAGFlowDocumentFailedWithAutoReparse`
  远端失败时写入 `failed/progress/last_error`，并根据当前 `auto_reparse_attempts` 设置下一次重试时间。
- `MarkRAGFlowDocumentAutoReparseQueued`
  自动重试提交成功后把文档改回 `queued`，清空 `last_error`，`auto_reparse_attempts + 1`，并清空
  `auto_reparse_next_at`。

手动重解析、上传新文件、覆盖行业库同名文件、整库重新解析时重置两个自动重试字段，避免历史重试次数影响新的人工操作。

## 后台任务流程

`RagflowParseStatusRefresher.Tick` 保持单一入口，内部拆成两个阶段：

1. `refreshQueuedAndRunningDocuments`
   负责现有状态同步；当远端失败且错误可重试时，写入 `failed` 和 `auto_reparse_next_at`。
2. `autoReparseDueFailedDocuments`
   查询已到期的失败文档，按远端 dataset 分组后调用 `ParseDocuments`，成功后写回 `queued`。

不新增单独的每轮自动重试数量配置。自动重试阶段只复用后台任务已有的批处理上限，避免一次 SQL 查询无限返回。

## 错误处理

- RAGFlow `ParseDocuments` 调用失败时，不增加 `auto_reparse_attempts`，保留原 `failed` 状态和错误，等待下一轮 tick。
- 某个 dataset 自动重试失败不阻断其它 dataset。
- 非白名单错误不设置 `auto_reparse_next_at`，不会进入自动重试阶段。
- 自动重试次数达到 3 后不再设置下一次重试时间。

## 测试

新增或更新单元测试覆盖：

- 可重试错误首次失败后自动进入 `queued`，自动重试次数变为 1。
- 第 1 次自动重试后再次失败，会设置 10 分钟后的 `auto_reparse_next_at`，未到期不重试。
- 到期后会提交第 2 次自动重试，并把状态改回 `queued`。
- 非白名单错误保持 `failed`，不自动重试。
- 自动重试次数达到 3 后不再重试。
- RAGFlow `ParseDocuments` 返回错误时不增加次数，保留失败状态。

相关验证命令：

```bash
go test ./internal/service -run 'TestRagflowParseStatusRefresher|TestExtractRAGFlowError'
go test ./internal/store ./internal/migrations
go test ./internal/...
```
