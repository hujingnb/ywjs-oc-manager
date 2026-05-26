# 设计文档：RAGFlow 替换现有知识库主库

**日期：** 2026-05-26
**状态：** 待用户复核

## 背景

当前知识库以 manager 本地文件系统为主副本：

- manager 将文件写入 `knowledge_root`；
- worker 将组织 / 实例知识库同步到 runtime 节点；
- Hermes 启动时把 `resources/knowledge/{org,app}` 渲染为 `skills/kb-*`；
- 知识库文件树、同步状态和容器重启共同决定知识库何时生效。

这个模型带来两类成本：manager 与 RAG 引擎之间需要文件同步，Hermes 也只能通过本地文件快照读取知识库。新方案用 RAGFlow 作为唯一文件主库，删除旧本地知识库主库、文件树、节点同步和 `kb-*` 渲染链路。

## 目标

- RAGFlow 成为知识库文件、解析状态、chunk 与检索索引的唯一主库。
- manager 继续负责 oc-manager 权限、审计、组织 / 实例边界、管理 UI 和文件生命周期。
- Hermes 能读取“当前实例知识库 + 所属组织知识库”。
- Hermes 能把工作目录中的产物写入当前实例知识库，例如用户要求“把这份报告加入知识库”。
- Hermes 对组织知识库只有只读检索权限。
- 组织知识库和实例知识库保留上传、下载、删除、重新解析能力。
- manager UI 改为扁平文件列表，不再展示文件树。

## 非目标

- 不迁移现有 `knowledge_root` 中的旧文件；启用新方案后旧本地知识库视为废弃。
- 不保留旧本地知识库实现、同步 dispatcher、`knowledge_sync_node`、`knowledge_sync_status` 前端展示和 `kb-*` 渲染兼容链路。
- 第一版不做文件预览，只保留下载。
- 第一版不做自动解析重试；解析失败后由用户在列表中手动点击重新解析。
- 不把 RAGFlow API key 暴露给前端浏览器。

## 推荐方案

采用“RAGFlow 主库 + manager 管理面 + Hermes MCP 检索 + manager 实例写入 API”的方案。

1. manager 通过 RAGFlow HTTP API 管理 dataset、document、下载、删除、解析和解析状态。
2. Hermes 通过 RAGFlow MCP host mode 做只读检索。
3. Hermes 不直连 RAGFlow HTTP API；容器网络只允许访问 RAGFlow MCP endpoint。
4. Hermes 写实例知识库时调用 manager 内部 runtime API，由 manager 校验实例 runtime token 后写入对应 app dataset。
5. manager 用自身 RAGFlow 管理凭证执行所有 RAGFlow HTTP 写操作。

这个拆分能同时满足：

- 组织知识库对 Hermes 只读；
- 实例知识库对 Hermes 可读可写；
- 管理面仍保留完整文件生命周期；
- 不需要实现 manager MCP proxy。

## RAGFlow 权限模型

RAGFlow MCP 当前提供 `ragflow_retrieval` 检索工具，按请求携带的 API key 决定可访问 dataset。RAGFlow dataset 权限为租户级：

- `permission = "me"`：只有 owner tenant 可访问。
- `permission = "team"`：加入该 tenant 的用户可访问。

源码与文档显示，RAGFlow API token 是 tenant 级 token，不是 dataset 级只读 token。`team` dataset 也不是严格只读能力：部分文档、chunk、dataset 写接口使用 `KnowledgebaseService.accessible` 或 `check_kb_team_permission` 判权，team 成员可能具备写路径。因此不能让 Hermes 用同一凭证同时访问 RAGFlow MCP 和 RAGFlow HTTP API。

### 租户与凭证

manager 维护 RAGFlow 侧身份映射：

- 每个 oc-manager 组织对应一个 RAGFlow org service user / tenant。
- 每个 oc-manager 实例对应一个 RAGFlow app service user / tenant。
- 每个 app service user 加入其所属 org service tenant。
- org dataset 创建在 org tenant 下，`permission = "team"`。
- app dataset 创建在 app tenant 下，`permission = "me"`。
- Hermes 使用 app service user 的 RAGFlow API token 连接 RAGFlow MCP host mode。

这样 Hermes 通过 MCP 可检索：

- app tenant 自己的实例 dataset；
- 所属 org tenant 暴露为 `team` 的组织 dataset。

但 Hermes 不能访问 RAGFlow HTTP API，所以无法写组织 dataset。

### 自动创建前置验证

RAGFlow 公开文档说明 API key 可从 UI 获取；主仓库源码存在 `/api/v1/system/tokens`，可为当前登录用户所属 tenant 创建 API token。实现计划必须先在目标 RAGFlow 版本上做真实验证：

- 能否自动创建 service user；
- 能否自动登录或以受控方式取得创建 token 所需的 session / auth；
- 能否自动创建 tenant API token；
- app service user 加入 org tenant 后，MCP host mode 是否只列出 app dataset + org team dataset；
- Hermes 网络隔离后，app token 是否无法访问 RAGFlow HTTP API。

如果目标 RAGFlow 版本不支持自动 service user/token 生命周期，则不直接进入主体实现，需要在以下两条路线中选择一条：

- 给 RAGFlow 增加受控管理接口；
- 回退到 manager 代理检索 / manager MCP proxy。

## 数据模型

新增 RAGFlow 映射表，manager 只保存映射、状态和审计所需元数据，不保存文件主副本。

### `ragflow_identities`

记录 oc-manager 组织 / 实例与 RAGFlow service user / tenant / token 的关系。

- `scope_type`：`org` 或 `app`。
- `org_id`：oc-manager 组织 ID。
- `app_id`：实例级身份时填写。
- `ragflow_user_id`：RAGFlow 用户 ID。
- `ragflow_tenant_id`：RAGFlow tenant ID。
- `api_token_ciphertext`：加密保存的 RAGFlow app service token，仅用于注入 Hermes MCP。
- `status`：`active`、`provisioning`、`failed`。
- `last_error`：最近一次 RAGFlow 身份或 token 操作失败原因。

### `ragflow_datasets`

记录组织 / 实例 dataset 映射。

- `scope_type`：`org` 或 `app`。
- `org_id`：oc-manager 组织 ID。
- `app_id`：实例级 dataset 时填写。
- `ragflow_dataset_id`：RAGFlow dataset ID。
- `ragflow_tenant_id`：dataset owner tenant。
- `permission`：`team` 或 `me`。
- `name`：RAGFlow dataset 名称。
- `status`：`active`、`creating`、`deleting`、`failed`。
- `last_error`：最近一次 dataset 生命周期失败原因。

### `ragflow_documents`

记录文件列表所需元数据。

- `dataset_id`：本地 `ragflow_datasets` 行 ID。
- `scope_type`、`org_id`、`app_id`：冗余保存，方便权限过滤。
- `ragflow_document_id`：RAGFlow document ID。
- `name`、`size`、`mime_type` / `suffix`。
- `parse_status`：`queued`、`running`、`completed`、`failed`、`stopped`。
- `progress`：解析进度。
- `last_error`：解析或同步状态失败原因。
- `created_by`：触发上传的 manager 用户或 app runtime 标识。

## Manager API 与 UI

### 组织知识库

组织知识库页面仍是独立 `/knowledge` 页面，但改为扁平列表。

API 能力：

- 列表：分页、搜索、状态筛选。
- 上传：上传到组织 dataset，并触发解析。
- 下载：由 manager 代理 RAGFlow 下载。
- 删除：删除 RAGFlow document 并删除本地映射。
- 重新解析：仅允许 failed / stopped 文档重新解析。

权限：

- 读取 / 下载遵循现有组织知识库读权限。
- 上传 / 删除 / 重新解析只允许组织管理员或平台管理员按现有权限规则操作。
- 权限谓词继续放在 `internal/auth/authorizer.go`。

### 实例知识库

实例详情知识库 tab 改为扁平列表。

API 能力：

- 列表、上传、下载、删除、重新解析。
- Hermes runtime 写入实例知识库也复用同一 service 层，但使用内部 runtime API 入口。

权限：

- 有实例读取权限的用户可查看 / 下载。
- 有实例管理权限的用户可上传 / 删除 / 重新解析。
- Hermes runtime token 只允许写当前 app dataset，不能写组织 dataset、其他 app dataset 或任意传入的 dataset ID。

### UI 行为

列表列：

- 文件名；
- 大小；
- 类型；
- 解析状态；
- 解析进度 / 错误；
- 上传时间；
- 操作：下载、删除、重新解析。

交互：

- 上传完成表示文件已进入 RAGFlow 且解析已触发，不等待解析完成。
- 解析状态异步刷新。
- 解析失败显示错误与重新解析按钮。
- 不显示文件树、路径、节点同步状态或容器重启提示。

## Hermes 集成

### 检索

应用 input / manifest 增加 RAGFlow MCP 配置：

- MCP endpoint；
- app service API token；
- 当前 app dataset ID；
- 当前 org dataset ID；
- 工具说明中强调：优先使用 app dataset，再使用 org dataset；组织知识库为只读参考资料。

Hermes 运行时通过 RAGFlow MCP host mode 调用 `ragflow_retrieval`。RAGFlow MCP 依据 app service token 返回可见 dataset 列表与检索结果。

旧的 `resources/knowledge/{org,app}` 输入、`skills/kb-*` 渲染和知识库变更后重启加载逻辑删除。

### 写实例知识库

Hermes 镜像内新增一个受控工具或命令，例如：

```text
oc-kb add <workspace-relative-file>
```

行为：

1. Hermes 根据用户指令选择工作目录中的文件。
2. 工具校验路径必须位于 `/opt/data/workspace` 下，拒绝任意绝对路径、父目录穿越和目录上传。
3. 工具使用 app runtime token 调 manager 内部 API。
4. manager 根据 token 解析 app ID，只允许写该 app 的实例 dataset。
5. manager 上传文件到 RAGFlow HTTP API，触发解析，写入 `ragflow_documents`。
6. 工具返回 document 名称、解析状态和后续可查询提示。

该写入通道不接受 `org_id`、`dataset_id` 或任意目标参数，避免 Hermes 通过提示注入选择组织 dataset。

## RAGFlow 生命周期

### 组织创建

1. manager 创建 oc-manager 组织。
2. 创建 RAGFlow org service user / tenant。
3. 创建 org dataset，`permission = "team"`。
4. 写入 `ragflow_identities` 和 `ragflow_datasets`。

组织删除时删除对应 RAGFlow dataset 与 service identity。RAGFlow 删除失败不阻塞本地删除，但写 audit log 并保留失败状态用于排障。

### 实例创建

1. manager 创建 app。
2. 创建 RAGFlow app service user / tenant。
3. 将 app service user 加入 org tenant。
4. 创建 app dataset，`permission = "me"`。
5. 创建 app service API token。
6. app 初始化时把 MCP endpoint、app token 和 dataset ID 写入 Hermes input。

实例删除时删除 app dataset、app service token / identity 映射。RAGFlow 删除失败不阻塞本地删除，但记录 audit。

### 文件上传

manager UI 上传：

1. handler 校验用户权限。
2. service 解析目标 scope。
3. RAGFlow client 上传 document。
4. manager 写 `ragflow_documents`。
5. manager 触发解析。
6. 返回文档行，解析状态异步刷新。

Hermes 上传：

1. `oc-kb add` 读取 workspace 文件。
2. runtime API 校验 app token。
3. service 固定选择 app dataset。
4. 后续流程与 manager UI 上传一致。

## 后台任务与错误处理

新增状态刷新任务：

- 扫描 `parse_status in ('queued', 'running')` 的文档；
- 调 RAGFlow 文档列表 / 状态接口刷新进度；
- 到达 completed / failed / stopped 后停止轮询；
- 失败写 `last_error`，保留下一轮重试。

错误策略：

- RAGFlow 未配置：知识库页面展示明确错误；server 不因缺配置启动失败。
- dataset 创建失败：组织 / 实例仍可创建，但知识库状态显示 failed，允许管理员重试初始化。
- 上传失败：不写本地 document 映射，返回用户可理解的错误。
- 删除 RAGFlow document 返回 404：按幂等成功处理，并删除本地映射。
- 解析失败：不自动重试，列表显示重新解析入口。
- Hermes runtime 写入失败：工具返回失败原因，不伪造“已加入知识库”。

## 安全约束

- RAGFlow 管理 token 只保存在 manager 后端配置中。
- RAGFlow app service token 加密存储，仅写入对应 app 的 Hermes input。
- Hermes 容器网络只能访问 RAGFlow MCP endpoint 和 manager runtime API，不能访问 RAGFlow HTTP API。
- manager runtime API 使用 per-app token，token 只授权当前 app 的实例知识库写入。
- runtime API 不接受外部传入 dataset ID。
- 所有 manager 用户侧权限仍由 `internal/auth/authorizer.go` 统一判断。
- 所有 RAGFlow 生命周期失败写 audit log，便于追踪跨系统状态。

## 改动范围

后端：

- 新增 RAGFlow client，封装 dataset、document、download、parse、status、token / identity 验证调用。
- 新增 RAGFlow identity / dataset / document 表和 sqlc 查询。
- 替换 `KnowledgeService` 为 RAGFlow-backed 实现。
- 删除本地 `KnowledgeMaster` 主副本装配、知识库 sync dispatcher、sync status service 在知识库页面的依赖。
- 修改 app 初始化 input，加入 MCP 配置并停止写 knowledge resources。
- 新增 Hermes runtime 写入实例知识库的 manager internal API。

前端：

- 组织知识库页面改为扁平列表。
- 实例知识库 tab 改为扁平列表。
- 删除文件树、路径输入、同步状态展示。
- 保留上传进度、下载、删除、重新解析。

OpenAPI：

- 修改知识库 handler 契约后必须运行 `make openapi-gen` 和 `make web-types-gen`。
- 不手工编辑 `openapi/openapi.yaml` 与 `web/src/api/generated.ts`。

文档：

- 更新 `docs/architecture.md`、`docs/hermes-container.md`、`docs/configuration.md`、`docs/user-manual.md`。
- 删除或改写本地知识库主副本、节点同步、`kb-*` 渲染相关描述。

## 测试计划

### 后端单元测试

- RAGFlow client：dataset 创建 / 删除、document 上传 / 下载 / 删除、解析触发、状态刷新、404 幂等。
- 权限：组织知识库写权限、实例知识库写权限、runtime token 只能写当前 app。
- 生命周期：组织创建 / 删除、app 创建 / 删除时 RAGFlow 映射与失败状态。
- Hermes 写入：workspace 路径校验、禁止父目录穿越、禁止传入任意 dataset。

### 前端测试

- 组织知识库扁平列表、状态筛选、上传、删除、重新解析按钮状态。
- 实例知识库扁平列表。
- 解析失败时显示错误并允许重新解析。
- 删除旧文件树交互后的空状态和错误态。

### Hermes runtime 测试

- MCP 配置渲染到 Hermes input / config。
- `oc-kb add` 只能读取 workspace 文件。
- `oc-kb add` 上传成功后 manager 生成实例 document 记录。
- Hermes 无法访问 RAGFlow HTTP API。

### 浏览器验证

完成实现后必须用真实浏览器验证：

- 组织管理员上传组织知识库，实例对话能检索到但不能通过 Hermes 写组织知识库。
- 有实例管理权限的用户上传实例知识库。
- 用户让 Hermes 把 workspace 中的报告加入实例知识库，列表出现该文件并进入解析状态。
- 解析完成后，Hermes 能通过 MCP 检索到新加入报告内容。

## 风险与取舍

- RAGFlow service user / token 自动化接口并非公开 HTTP API 文档中的核心管理接口，实现前必须用目标版本验证。
- RAGFlow `team` dataset 不能表达“只读成员”，所以网络隔离是组织知识库只读的关键安全边界。
- Hermes 写实例知识库通过 manager runtime API 多一跳，但这是保住权限边界的必要代价。
- 删除旧知识库链路会减少兼容成本，但也意味着启用新方案前需要明确告知旧本地文件不迁移。

## 参考

- RAGFlow HTTP API：`https://ragflow.com.cn/docs/http_api_reference`
- RAGFlow MCP：`https://ragflow.com.cn/docs/category/mcp`
- RAGFlow MCP tools：`https://ragflow.com.cn/docs/mcp_tools`
- RAGFlow MCP server：`https://ragflow.com.cn/docs/launch_mcp_server`
- RAGFlow API key：`https://ragflow.com.cn/docs/acquire_ragflow_api_key`
