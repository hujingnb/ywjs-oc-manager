# 设计文档：知识库批量上传

**日期：** 2026-06-03
**状态：** 待用户复核

## 背景

当前知识库管理面包含两个上传入口：

- 企业知识库：`/knowledge`
- 实例知识库：`/apps/:appId/knowledge`

两处入口目前都只能通过原生 `file input` 选择单个文件。后端契约也是单文件上传：浏览器以
`application/octet-stream` 请求体提交文件，并通过 `filename` query 传文件名。service 层在上传到
RAGFlow 前完成权限校验、单文件大小校验、累计容量校验和 RAGFlow dataset 选择。

前端已有全局 `uploadProgress` store，支持多文件串行上传、当前文件进度、取消、部分失败继续后续文件和结束汇总。批量上传应优先复用这条既有能力，避免为同一行为引入后端批量契约或额外任务系统。

## 目标

- 企业知识库和实例知识库都支持一次选择多个文件上传。
- 企业知识库和实例知识库都支持拖拽多个文件上传。
- 批量上传按文件顺序串行执行。
- 单个文件失败不阻塞后续文件。
- 上传进度、取消、成功 / 失败 / 取消汇总沿用全局 `UploadProgressModal`。
- 后端继续作为权限、容量和 RAGFlow 写入的最终裁决者。
- 文档同步说明多选、拖拽、串行上传和失败处理语义。

## 非目标

- 不新增后端批量上传 API。
- 不把多个文件打包成一个 multipart 请求。
- 不引入后台 job、断点续传、暂停 / 恢复或并发上传。
- 不支持文件夹递归上传。
- 不改变 RAGFlow client、service 层容量校验或 OpenAPI 契约。
- 不改变知识库列表、下载、删除、重解析逻辑。

## 已确认决策

用户已确认以下范围和取舍：

- 覆盖两个入口：企业知识库和实例知识库。
- 上传方式：多选文件 + 拖拽上传。
- 执行策略：串行上传。
- 错误策略：逐个上传逐个失败；前端只保留单文件上限这类即时拦截，容量不足等由后端逐个返回。
- 推荐方案：前端批量编排，复用现有单文件 API。

## 备选方案

### 方案一：前端批量编排，复用单文件 API

前端把多选或拖拽得到的 `File[]` 转为 `uploadProgress.run` 的 items。runner 按既有方式逐个调用
`useUploadOrgKnowledge` 或 `useUploadAppKnowledge`，每个文件仍走当前单文件 API。

优点：

- 不改后端契约和 OpenAPI。
- 不改 RAGFlow 集成。
- 复用现有上传进度、取消和失败汇总能力。
- 与“串行上传、逐个失败”的产品语义一致。

代价：

- 总耗时是所有文件上传耗时累加。
- 浏览器会发起多个 HTTP 请求。

这是本设计采用的方案。

### 方案二：新增后端批量上传 API

新增 multipart 多文件接口，由后端逐个上传 RAGFlow 并返回批量结果。

优点是前端请求数更少，接口语义更像“批量上传”。代价是需要改 handler、service、RAGFlow 调用、OpenAPI、前端类型和测试，并且大文件 multipart 对内存、超时和网关限制更敏感。本次不采用。

### 方案三：后台任务式批量上传

前端创建批量上传任务，后端异步处理文件并暴露任务进度。

优点是适合超大批次和长时间上传。代价是需要文件暂存、任务状态、清理和轮询机制，明显超出本需求。本次不采用。

## 交互设计

### 多选上传

两处知识库页面保留现有“上传文件”按钮，但将隐藏的文件选择器改为 `multiple`。用户一次选择多个文件后，页面按选择顺序创建上传队列并打开全局上传进度弹窗。

弹窗继续展示：

- 当前文件名；
- 当前文件在批次中的序号，格式为 `N/M`；
- 当前文件上传字节进度；
- 取消上传按钮；
- 结束后的成功、失败、取消数量；
- 失败详情中的文件名和错误文案。

### 拖拽上传

有写权限时，知识库文件列表卡片响应拖拽文件上传。用户把一个或多个文件拖到卡片区域后，页面把拖拽得到的文件交给同一批量上传流程。

无写权限时不显示上传入口，也不响应拖拽上传。拖拽上传不改变列表中的下载、删除、重解析等操作。

拖拽只支持文件，不支持文件夹递归。浏览器能识别为目录的拖拽项应被忽略；如果拖拽结果没有任何文件，则不创建上传会话。

## 架构与数据流

后端继续保留现有单文件写入口：

- `POST /api/v1/organizations/{orgId}/knowledge?filename=<name>`
- `POST /api/v1/apps/{appId}/knowledge?filename=<name>`

前端改动集中在：

- `web/src/pages/knowledge/OrgKnowledgePage.vue`
- `web/src/pages/apps/AppKnowledgeTab.vue`
- 可选的轻量 helper 或 composable，用于复用文件收集、单文件上限过滤和批量上传编排。

数据流：

```text
file input multiple / drop files
  -> File[]
  -> 过滤超过单文件上限的文件并提示
  -> uploadProgress.run(items, runner)
  -> runner 逐个调用 useUploadOrgKnowledge / useUploadAppKnowledge
  -> xhrUpload 单文件上传
  -> handler/service 执行权限、Content-Length、容量和 RAGFlow 上传
  -> mutation onSettled 刷新知识库列表
```

取消语义沿用现有 `uploadProgress` store：用户取消当前文件后，当前文件标记为 `cancelled`，后续尚未上传的文件也标记为 `cancelled`，不再继续发请求。

## 错误处理与边界

前端选择或拖拽阶段只处理以下情况：

- 空文件列表：直接忽略。
- 单文件超过现有上传上限：该文件不进入上传队列，并使用 `KNOWLEDGE_UPLOAD_MAX_MESSAGE` 提示。
- 全部文件都被过滤：不创建上传会话。
- 已有上传会话：沿用现有“已有上传任务正在进行”提示。

容量不足、权限变化、RAGFlow 错误、网络错误等不在前端提前预判。每个文件上传时由后端实时校验，失败后进入全局上传弹窗的失败详情。批量中前几个文件成功后会占用容量，后续文件可能返回 `KNOWLEDGE_QUOTA_EXCEEDED`，这是预期行为。

现有代码中的单文件上限为 `1GB`，前端提示由 `KNOWLEDGE_UPLOAD_MAX_MESSAGE` 推导，后端提示由 `maxKnowledgeUploadBytes` 推导。文档里仍有旧的 `100MB` 说法，实施时应同步改为现有上限，避免用户手册和实际限制不一致。

## 测试计划

前端页面测试：

- `OrgKnowledgePage.spec.ts` 覆盖多选文件会把多个文件传给 `uploadProgress.run`。
- `OrgKnowledgePage.spec.ts` 覆盖拖拽多个文件进入同一批上传流程。
- `OrgKnowledgePage.spec.ts` 覆盖超过单文件上限的文件被前端拦截。
- `AppKnowledgeTab.spec.ts` 覆盖实例知识库多选文件进入批量上传流程。
- `AppKnowledgeTab.spec.ts` 覆盖实例知识库拖拽上传进入批量上传流程。
- `AppKnowledgeTab.spec.ts` 覆盖超过单文件上限的文件被前端拦截。

既有 `uploadProgress` store 已覆盖批量串行、部分失败继续、取消和会话互斥，不重复测试同一内部状态机。若新增 helper 或 composable，则为 helper 增加聚焦单测，覆盖文件收集、目录忽略和上限过滤。

后端接口和 OpenAPI 不变，因此不需要 `make openapi-gen` 或 `make web-types-gen`。实现完成后需要运行相关前端单测；如果触碰 helper 或文档，还应运行对应测试或说明无需运行的原因。

## 文档计划

实施时同步更新：

- `docs/knowledge-base.md`：管理后台上传说明改为支持多选和拖拽；说明批量上传串行执行，失败项不阻塞后续文件。
- `docs/user-manual.md`：企业知识库和实例知识库章节同步更新上传操作描述。

文档应同时修正旧的 `100MB` 上传上限文案，以现有代码的 `1GB` 单文件上限为准。

## 验收标准

- 企业知识库页面可一次选择多个文件并串行上传。
- 实例知识库页面可一次选择多个文件并串行上传。
- 企业知识库页面可拖拽多个文件并串行上传。
- 实例知识库页面可拖拽多个文件并串行上传。
- 单个文件失败不会阻塞后续文件。
- 上传取消后当前文件和后续待上传文件按现有弹窗语义显示为取消。
- 批量上传完成后知识库列表刷新。
- 无写权限用户看不到上传入口，拖拽文件不会触发上传。
- 后端 API、OpenAPI 和 RAGFlow client 无契约变更。
