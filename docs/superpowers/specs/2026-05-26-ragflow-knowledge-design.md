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
- Hermes 能通过 manager 提供的知识库 skill 读取“当前实例知识库 + 所属组织知识库”。
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
- 第一版不引入 MCP 作为 Hermes 知识库接入层；RAGFlow MCP 只作为后续可选 POC。
- 第一版不由 manager 管理 RAGFlow 的模型供应商、RAGFlow 内部 LLM 配置或 RAGFlow 调 new-api 的 API key。

## 推荐方案

采用“RAGFlow 主库 + manager 管理面 + Hermes 知识库 skill”的方案。

1. manager 通过 RAGFlow HTTP API 管理 dataset、document、下载、删除、解析和解析状态。
2. Hermes 通过知识库 skill 调用 manager runtime API 做检索。
3. Hermes 写实例知识库时也通过知识库 skill 调用 manager runtime API。
4. manager 根据实例 runtime token 固定解析当前 app 与所属 org，决定可读 / 可写 dataset。
5. manager 用自身 RAGFlow 管理凭证执行所有 RAGFlow HTTP 检索和写操作。
6. RAGFlow 的模型供应商由管理员在 RAGFlow 控制台手工配置，指向现有 new-api，并使用 DeepSeek 模型。

这个拆分能同时满足：

- 组织知识库对 Hermes 只读；
- 实例知识库对 Hermes 可读可写；
- 管理面仍保留完整文件生命周期；
- 不依赖 RAGFlow MCP 或 RAGFlow 自身权限模型。

## 权限边界

RAGFlow 第一版只作为 manager 后端依赖，不参与 oc-manager 的业务权限管理。Hermes 不持有 RAGFlow API key，不接收 RAGFlow dataset ID，也不访问 RAGFlow MCP endpoint 或 RAGFlow HTTP API。

“组织只读、实例读写”的权限全部由 manager runtime API 统一控制：

- 检索时，manager 只使用 runtime token 解析出的当前 app dataset 与所属 org dataset；
- 写入时，manager 固定选择当前 app dataset；
- runtime API 不接受外部传入的 `dataset_id`、`org_id` 或任意目标参数。

如果 RAGFlow HTTP API 在创建 dataset 时要求额外可见性字段，manager 的 RAGFlow client 只把它当作外部 API 兼容参数处理，并固定为最小可见范围；该字段不进入 oc-manager 的权限设计、权限判断或安全边界。

### RAGFlow 凭证

本方案涉及两类凭证，职责必须分开：

- manager 调 RAGFlow HTTP API 的 RAGFlow API key：只保存在 manager 后端配置或密钥存储中。
- RAGFlow 调 new-api 的模型 API key：由管理员在 RAGFlow 控制台手工创建 / 配置，使用现有 new-api 和 DeepSeek 模型；manager 不读取、不保存、不轮换该 key。
- oc-manager 通过本地映射表维护 org / app 与 RAGFlow dataset 的关系。
- Hermes 不持有任何 RAGFlow 凭证。

这样做的核心取舍是：RAGFlow 负责文件主库、解析和检索能力；oc-manager 负责业务权限、实例隔离和可写目标控制。

### RAGFlow 模型配置

RAGFlow 使用现有 new-api 作为模型供应商。部署时由管理员打开 RAGFlow 控制台完成：

1. 在 new-api 中为 RAGFlow 创建专用 API key；
2. 在 RAGFlow 的模型供应商配置中填写 new-api base URL 和该 API key；
3. 在 RAGFlow 中选择 DeepSeek 模型作为解析 / embedding / retrieval 所需模型，具体字段以目标 RAGFlow 版本的控制台为准；
4. 在启用 oc-manager 知识库前，用 RAGFlow 控制台或 API 验证文档解析与 retrieval 能成功跑通。

manager 不自动创建 new-api key，不自动修改 RAGFlow 模型配置，也不把 oc-manager 里现有的 new-api 用户 token 复用给 RAGFlow。

### 自动创建前置验证

第一版只要求验证目标 RAGFlow 版本能通过 HTTP API 自动完成 dataset 与 document 生命周期：

- 创建组织 dataset；
- 创建实例 dataset；
- 上传、下载、删除 document；
- 触发解析并读取解析状态；
- 对指定 dataset 执行 retrieval。

RAGFlow 公开文档说明 API key 可从 UI 获取；第一版只需要一个 manager 后端使用的 RAGFlow API key。自动创建 RAGFlow 内部用户或 token 不再是第一版目标，也不作为本方案的权限基础。

## Docker Compose 与部署形态

RAGFlow 引入后，Compose 需要同时服务本地联调和生产拆分部署。两种形态共享同一套业务边界：manager 只通过 RAGFlow HTTP API 访问 RAGFlow，不加入 RAGFlow 内部依赖网络，也不直接连接 RAGFlow MySQL、Redis/Valkey、MinIO 或 Elasticsearch。

### 本地调试 Compose

仓库根目录 `docker-compose.yml` 继续作为本地一键联调环境，并在其中加入 RAGFlow 及其依赖：

- `ragflow`：RAGFlow 主服务，暴露 Web 控制台、HTTP API 和 Admin API；
- `ragflow-mysql`：RAGFlow 自身账号、dataset 与任务元数据；
- `ragflow-redis`：RAGFlow 内部队列和缓存；
- `ragflow-minio`：上传原文件与解析中间产物；
- `ragflow-es`：默认检索与向量索引后端。

本地 manager 使用 Compose service name 访问 RAGFlow：

```yaml
ragflow:
  base_url: "http://ragflow:9380"
```

本地 `.env` 允许配置 RAGFlow 端口、镜像和依赖密码。默认密码仅用于开发联调，不能进入生产配置。RAGFlow 调用 new-api 的模型供应商仍由管理员在 RAGFlow 控制台手工配置；本地 Compose 只保证网络上同时存在 `new-api`、`ollama` 和 `ragflow`，不自动写 RAGFlow 模型配置。

### 生产 Compose

生产保持运行包拆分：

- `deploy/ragflow/docker-compose.yml` 独立启动 RAGFlow、MySQL、Redis/Valkey、MinIO 和 Elasticsearch；
- `deploy/manage/docker-compose.yml` 只启动 manager-api、manager-web、nginx、PostgreSQL 和 Redis；
- manager 不加入 `ragflow-internal` 网络，只通过 `ragflow.base_url` 访问 RAGFlow HTTP API。

同机部署时，`deploy/manage/config/manager.yaml` 使用宿主机端口访问独立 RAGFlow：

```yaml
ragflow:
  base_url: "http://host.docker.internal:9380"
```

异机部署时，`base_url` 改为 RAGFlow 服务器的内网地址或 HTTPS 入口。生产必须固定 RAGFlow 和依赖镜像 tag 或 digest，RAGFlow 的 MySQL、Redis/Valkey、MinIO、Elasticsearch 数据目录由 `deploy/ragflow` 独立备份。

### 端口与网络要求

- RAGFlow Web 控制台只面向管理员开放，用于创建 RAGFlow API key 和配置 new-api + DeepSeek 模型供应商。
- RAGFlow HTTP API 必须允许 manager 服务器访问。
- RAGFlow Admin API 仅用于运维排障，不下发给 Hermes。
- RAGFlow 内部 MySQL、Redis/Valkey、MinIO、Elasticsearch 不对 manager 或公网开放。
- Hermes 容器只需要访问 manager runtime API 和 new-api OpenAI-compatible endpoint，不需要访问 RAGFlow HTTP API 或 RAGFlow 内部网络。

### 启动顺序

本地联调可以直接使用根目录 Compose 启动全套服务。生产推荐顺序为：

1. 启动 Ollama 并验证模型可用；
2. 启动 new-api，配置模型渠道并创建给 RAGFlow 使用的专用 API key；
3. 启动 `deploy/ragflow`，在 RAGFlow 控制台配置 new-api + DeepSeek，并创建 manager 专用 RAGFlow API key；
4. 启动 `deploy/manage`，配置 `ragflow.base_url` 与 `ragflow.api_key`；
5. 启动 runtime-agent 节点。

## 数据模型

新增 RAGFlow 映射表，manager 只保存映射、状态和审计所需元数据，不保存文件主副本。RAGFlow 后端凭证作为 manager 配置或密钥保存，不按组织 / 实例下发给 Hermes。

### `ragflow_datasets`

记录组织 / 实例 dataset 映射。

- `scope_type`：`org` 或 `app`。
- `org_id`：oc-manager 组织 ID。
- `app_id`：实例级 dataset 时填写。
- `ragflow_dataset_id`：RAGFlow dataset ID。
- `name`：RAGFlow dataset 名称。
- `status`：`active`、`creating`、`deleting`、`failed`。
- `last_error`：最近一次 dataset 生命周期失败原因。
- `create_claim_token`：dataset 创建租约，避免并发创建或进程崩溃后重复写入远端 dataset。

### `apps` runtime token 字段

Hermes 通过 per-app runtime token 调 manager runtime API。token 明文只写入 Hermes input，不以明文保存在 manager 数据库。

- `runtime_token_hash`：用于 runtime API 认证查找的 token 哈希，未删除 app 内唯一。
- `runtime_token_ciphertext`：加密后的 token 明文，用于重建 Hermes input 或重启实例时重新写入 manifest。

### `ragflow_documents`

记录文件列表所需元数据。

- `dataset_id`：本地 `ragflow_datasets` 行 ID。
- `scope_type`、`org_id`、`app_id`：冗余保存，方便权限过滤。
- `ragflow_document_id`：RAGFlow document ID。
- `name`、`size`、`mime_type` / `suffix`。
- `parse_status`：`queued`、`running`、`completed`、`failed`、`stopped`。
- `progress`：解析进度。
- `last_error`：解析或状态刷新失败原因。
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

Hermes 使用知识库 skill 检索，不直接连接 RAGFlow。应用 input / manifest 增加 manager runtime 知识库配置：

- manager runtime API endpoint；
- app runtime token；
- 知识库 search 工具说明：优先使用实例知识库，再使用组织知识库；组织知识库为只读参考资料。

Hermes 运行时通过 skill 调用 manager runtime search API。manager 根据 runtime token 解析 app ID 和 org ID，固定选择当前 app dataset 与所属 org dataset，然后调用 RAGFlow retrieval API。

为满足“实例知识库优先”，manager 第一版采用两路检索：

1. 对 app dataset 单独 retrieval；
2. 对 org dataset 单独 retrieval；
3. 合并结果时保留来源 scope，并对 app 结果排序优先。

旧的 `resources/knowledge/{org,app}` 输入、`skills/kb-*` 渲染和知识库变更后重启加载逻辑删除。

### 写实例知识库

Hermes 镜像内新增知识库写入 skill 或受控命令，例如：

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
2. 使用 manager 后端 RAGFlow 凭证创建 org dataset。
3. 写入 `ragflow_datasets`。

组织删除时删除对应 RAGFlow dataset。RAGFlow 删除失败不阻塞本地删除，但写 audit log 并保留失败状态用于排障。

### 实例创建

1. manager 创建 app。
2. 使用 manager 后端 RAGFlow 凭证创建 app dataset。
3. 写入 `ragflow_datasets`。
4. app 初始化时把 manager runtime API endpoint、app runtime token 和知识库 skill 配置写入 Hermes input。

实例删除时删除 app dataset。RAGFlow 删除失败不阻塞本地删除，但记录 audit。

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

新增状态刷新任务 `ragflow_parse_status_refresh`：

- 周期 30 秒触发，批量扫描 `parse_status in ('queued', 'running')` 的文档（默认 100 条 / 轮）；
- 按所属远端 dataset 分组，每个 dataset 只调用一次 RAGFlow `ListDocuments`，减少请求次数；
- 远端状态归一化后回写本地 `parse_status` 与 `progress`；到达 completed / failed / stopped 后下一轮不再被选中；
- 单 dataset 拉取失败时，组内文档保留原 `parse_status`、把失败原因写入 `last_error`，等下一轮重试，其他 dataset 不受影响；
- 远端 ListDocuments 中找不到的文档视为外部已删除，本地标记 failed 并写入提示，避免列表永远 stuck running。

列表请求只读本地缓存，不再向 RAGFlow 同步拉状态，避免列表延迟受 RAGFlow 可用性影响，也避免无人查看列表时状态永不刷新。

错误策略：

- RAGFlow 未配置：知识库页面展示明确错误；server 不因缺配置启动失败。
- dataset 创建失败：组织 / 实例仍可创建，但知识库状态显示 failed，允许管理员重试初始化。
- 上传失败：不写本地 document 映射，返回用户可理解的错误。
- 删除 RAGFlow document 返回 404：按幂等成功处理，并删除本地映射。
- 解析失败：不自动重试，列表显示重新解析入口。
- Hermes runtime 写入失败：工具返回失败原因，不伪造“已加入知识库”。

## 安全约束

- RAGFlow 管理 token 只保存在 manager 后端配置中。
- Hermes input 只包含 manager runtime API endpoint 和 app runtime token，不包含 RAGFlow API key 或 dataset ID。
- Hermes 容器网络只需要访问 manager runtime API，不需要访问 RAGFlow MCP endpoint 或 RAGFlow HTTP API。
- manager runtime API 使用 per-app token，token 授权当前 app 的知识库检索与实例知识库写入。
- runtime API 不接受外部传入 dataset ID。
- 所有 manager 用户侧权限仍由 `internal/auth/authorizer.go` 统一判断。
- 所有 RAGFlow 生命周期失败写 audit log，便于追踪跨系统状态。

## 改动范围

后端：

- 新增 RAGFlow client，封装 dataset、document、download、parse、status、retrieval 调用。
- 新增 RAGFlow dataset / document 表和 sqlc 查询。
- 替换 `KnowledgeService` 为 RAGFlow-backed 实现。
- 删除本地 `KnowledgeMaster` 主副本装配、知识库 sync dispatcher、sync status service 在知识库页面的依赖。
- 修改 app 初始化 input，加入知识库 skill 配置并停止写 knowledge resources。
- 新增 Hermes runtime 知识库 search / add 的 manager internal API。

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
- 权限：组织知识库写权限、实例知识库写权限、runtime token 只能读取当前 app + 所属 org，且只能写当前 app。
- 生命周期：组织创建 / 删除、app 创建 / 删除时 RAGFlow 映射与失败状态。
- Hermes 检索：search API 不接受任意 dataset，app 结果优先于 org 结果。
- Hermes 写入：workspace 路径校验、禁止父目录穿越、禁止传入任意 dataset。

### 前端测试

- 组织知识库扁平列表、状态筛选、上传、删除、重新解析按钮状态。
- 实例知识库扁平列表。
- 解析失败时显示错误并允许重新解析。
- 删除旧文件树交互后的空状态和错误态。

### Hermes runtime 测试

- 知识库 skill 配置渲染到 Hermes input / config。
- 知识库 search skill 只能通过 manager runtime API 检索当前 app + 所属 org。
- `oc-kb add` 只能读取 workspace 文件。
- `oc-kb add` 上传成功后 manager 生成实例 document 记录。
- Hermes 无法访问 RAGFlow HTTP API。

### 浏览器验证

完成实现后必须用真实浏览器验证：

- 组织管理员上传组织知识库，实例对话能检索到但不能通过 Hermes 写组织知识库。
- 有实例管理权限的用户上传实例知识库。
- 用户让 Hermes 把 workspace 中的报告加入实例知识库，列表出现该文件并进入解析状态。
- 解析完成后，Hermes 能通过知识库 search skill 检索到新加入报告内容。

## 风险与取舍

- Hermes 检索和写入都通过 manager runtime API 多一跳，但这是保住权限边界、降低 RAGFlow MCP 权限不确定性的必要代价。
- manager 成为知识库检索代理后，需要关注检索延迟、结果合并和引用来源展示。
- RAGFlow API key 获取方式依赖部署侧配置；第一版不实现 RAGFlow 内部账号 / token 自动化。
- 删除旧知识库链路会减少兼容成本，但也意味着启用新方案前需要明确告知旧本地文件不迁移。

## 参考

- RAGFlow HTTP API：`https://ragflow.com.cn/docs/http_api_reference`
- RAGFlow MCP：`https://ragflow.com.cn/docs/category/mcp`
- RAGFlow MCP tools：`https://ragflow.com.cn/docs/mcp_tools`
- RAGFlow MCP server：`https://ragflow.com.cn/docs/launch_mcp_server`
- RAGFlow API key：`https://ragflow.com.cn/docs/acquire_ragflow_api_key`
