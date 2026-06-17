# 知识库（RAG）

> 本文介绍 oc-manager 基于 RAGFlow 的知识库（RAG）能力：它解决什么问题、企业 / 实例 / 行业三类
> 知识库如何划分、文档怎么进（管理后台上传 + Hermes 自动加入）、Hermes 怎么检索（跨 scope、
> 实例优先）、解析状态如何流转、各角色权限边界，以及部署前置的 RAGFlow 模型配置。
> UI 逐步操作见 [用户手册](./user-manual.md)，容器内链路细节见 [Hermes 容器运行机制](./hermes-container.md)。

---

## 1. 设计目标与边界

知识库以 **RAGFlow 作为唯一文件主库**：文件原件、解析、chunk、向量索引与检索全部在 RAGFlow。
manager 不再保存文件主副本，只负责管理面、权限、审计、企业 / 实例边界和文件生命周期。

这样划分的结果：

- **manager**：通过 RAGFlow HTTP API 管理 dataset / document（上传、下载、删除、触发解析、读状态、检索），并用自身一把 RAGFlow 管理凭证执行所有 RAGFlow 操作。
- **Hermes 容器**：只通过 **manager runtime API** 检索和写入知识库，**不持有 RAGFlow API key，也不知道 RAGFlow dataset ID**，更不直连 RAGFlow。
- **RAGFlow**：作为 manager 的后端依赖，不参与 oc-manager 的业务权限判断。

> 核心取舍：RAGFlow 提供文件主库、解析与检索能力；oc-manager 负责业务权限、实例隔离和可写目标控制。「企业 / 行业只读、实例读写」这条权限完全由 manager runtime API 收敛，不依赖 RAGFlow 自身权限模型，也不引入 RAGFlow MCP。

---

## 2. 三类知识库（scope）

| scope | 归属 | 用途 | manager UI 入口 |
|---|---|---|---|
| 企业知识库（`org`）| 企业 | 企业内共享的政策 / 产品文档 / 通用资料 | `/knowledge` |
| 实例知识库（`app`）| 单个 Hermes 实例 | 该实例私有资料、Hermes 在对话中沉淀的工作产物 | `/apps/:appId/knowledge` |
| 行业知识库（`industry`）| 平台 | 助手版本选择的通用行业资料 | `/platform/industry-knowledge` |

每个企业对应一个 RAGFlow org dataset，每个实例对应一个 app dataset，映射关系记录在 manager 的
`ragflow_datasets` 表；每个行业知识库也对应一个 industry dataset，行业名称在未删除记录中唯一。实例知识库严格隔离：一个实例的文档不会出现在其它实例或企业的检索结果里。行业知识库不按企业隔离，是否参与检索由平台管理员在助手版本中显式选择。

---

## 3. 整体链路

```text
管理后台（人工管理）                         Hermes 容器（对话中读写）
┌───────────────────────┐                  ┌──────────────────────────────┐
│ Browser → Vue SPA     │                  │ Hermes Agent                 │
│        │ Bearer JWT    │                  │   └─ skills/oc-kb            │
│        ▼               │                  │        │ oc-kb search/add    │
│   manager-api          │                  │        ▼ X-OC-App-Token      │
│   (KnowledgeService)   │◄─────────────────┤   manager runtime API        │
│        │               │  同一 service 层 │   /api/v1/runtime/knowledge/*│
│        ▼ RAGFlow API key                  └──────────────────────────────┘
│   RAGFlow HTTP API ◄───────────────────────────────────┘
│   （dataset / document / parse / retrieval）
└───────────────────────┘
```

要点：

- 管理后台走用户侧 Bearer JWT，Hermes 走 per-app runtime token（`X-OC-App-Token`），两条入口在
  router 层就能区分，但最终都落到同一套 `KnowledgeService`。
- runtime API **不接受外部传入的 `dataset_id` / `org_id` 等目标参数**：manager 用 runtime token
  解析出当前 app、所属 org 与当前助手版本，自行决定可读 / 可写 dataset，防止通过提示注入跨 scope。

---

## 4. 怎么往知识库加文档

有三类入口，分别面向「人工管理」「外部商业知识库同步」和「Hermes 在对话中沉淀产物」。

### 4.1 管理后台上传（人工）

- 企业知识库：`/knowledge` → 右上角「上传文件」（仅企业管理员 / 平台管理员可见）。
- 实例知识库：`/apps/:appId/knowledge` → 「上传文件」（有实例管理权限的用户）。
- 行业知识库：`/platform/industry-knowledge` → 选择行业库后上传文件（仅平台管理员）。

行为：

- 支持一次选择多个文件，也支持把多个文件拖拽到知识库文件列表卡片区域。
- 批量上传按文件顺序串行执行；单个文件失败不会阻塞后续文件，结束后由上传进度弹窗汇总成功、失败和取消数量。
- 每个文件上传成功后都会进入对应 dataset 并**触发解析**；上传成功只表示「文件已进入 RAGFlow 且解析已触发」，
  **不等待解析完成**，解析状态后续异步刷新。
- 单文件上限 **1GB**（与后端 `maxKnowledgeUploadBytes` 和前端 `KNOWLEDGE_UPLOAD_MAX_BYTES` 保持一致）。

对应接口（完整契约见 [`openapi/openapi.yaml`](../openapi/openapi.yaml)）：

| 操作 | 方法与路径 |
|---|---|
| 企业库列表 | `GET /api/v1/organizations/{orgId}/knowledge` |
| 企业库上传 | `POST /api/v1/organizations/{orgId}/knowledge?filename=<name>`（`202 Accepted`）|
| 企业库删除 | `DELETE /api/v1/organizations/{orgId}/knowledge/{documentId}`（`204`）|
| 实例库列表 | `GET /api/v1/apps/{appId}/knowledge` |
| 行业库列表 | `GET /api/v1/industry-knowledge-bases` |
| 行业库文件上传 | `POST /api/v1/industry-knowledge-bases/{industryId}/knowledge?filename=<name>`（`202 Accepted`）|
| 外部行业库上传 | `POST /api/v1/external/industry-knowledge/files`（固定 token 鉴权）|

外部商业知识库同步入口通过 `industry_knowledge.upload_token` 配置固定鉴权字符串，请求需带
`X-OC-Industry-Knowledge-Token`。外部服务提交 `industry_name` 和文件；manager 会按行业名称找到或创建行业库，并把同名文件覆盖到该行业 dataset。

### 4.2 Hermes 自动加入（`oc-kb add`）

Hermes 镜像内置 `oc-kb` skill，可在对话中把工作目录里的产物加入**当前实例**知识库。典型场景：用户说
「把这份报告加入知识库」，Hermes 先在 `/opt/data/workspace` 写好文件，再执行：

```text
oc-kb add <workspace 相对路径> [--filename <名称>]
```

约束：

- 路径必须位于 `/opt/data/workspace` 下，拒绝绝对路径、`..` 父目录穿越和目录上传；
- 只写**当前实例** dataset，**不能写企业知识库或其它实例**；该通道不接受 `org_id` / `dataset_id`；
- 上传后 manager 写入 `ragflow_documents` 并触发解析，返回 document 名称与解析状态（`queued`）。
  上传失败会如实返回失败原因，不会伪造「已加入知识库」。

---

## 5. 解析状态（parse status）

文档解析由 RAGFlow 异步完成，状态在 manager 侧流转：

```text
queued ──► running ──► completed
                  └──► failed / stopped
```

| 状态 | 含义 |
|---|---|
| `queued` | 已提交解析，排队中 |
| `running` | RAGFlow 正在解析 |
| `completed` | 解析完成，可被检索 |
| `failed` | 解析失败（列表显示错误与「重解析」入口）|
| `stopped` | 解析被中止 |

刷新机制：

- 后台任务 `ragflow_parse_status_refresh` 每 **30 秒**批量扫描 `queued` / `running` 文档，按远端
  dataset 分组、每个 dataset 只调一次 RAGFlow `ListDocuments`，归一化后回写本地 `parse_status` / `progress`。
- **列表请求只读 manager 本地状态，不再实时回查 RAGFlow**：列表延迟不受 RAGFlow 可用性影响，也保证
  无人查看列表时状态仍会被后台推进。
- 第一版不自动重试解析；解析失败 / 已停止的文档在列表里手动点「重解析」重新提交。

---

## 6. Hermes 检索（跨 scope，实例优先）

Hermes 通过 `oc-kb search` 检索，不直连 RAGFlow：

```text
oc-kb search "<问题>" [--top-k 8]
```

- manager 用 runtime token 解析当前 app 与所属 org，再读取当前 app 绑定助手版本选择的行业库；检索顺序为
  **app → org → industry**，合并时保留来源 scope，并让实例（app）结果优先。
- 每个关联行业知识库都会单独检索并返回最多 `top_k` 条结果；如果一个助手版本关联很多行业库，返回给 Hermes 的上下文会线性膨胀。版本编辑页会提示该风险，由平台管理员自行判断关联数量。
- 检索结果带 `scope`（`app` / `org` / `industry`）、`document_name`、`similarity` 与 chunk 内容；行业命中还会返回 `industry_knowledge_base_id` / `industry_knowledge_base_name`，便于 Hermes 和排障识别来源。

**强制优先知识库**：容器启动时渲染的 `SOUL.md` 注入了知识库指引——对用户的**每一次提问**，Hermes 的
第一个动作都必须是 `oc-kb search`，不得跳过、不得先用网络搜索 / 记忆代替，哪怕它主观判断知识库里没有相关内容；
只有在检索返回后才决定是否动用其它信息源。这里刻意不让模型先判断「问题是否依赖知识库」（旧版「可能依赖才查」
会被模型一句「这问题应该不在库里」就跳过），因此用户无需显式说「知识库」，Hermes 也会主动检索。该指引仅在
实例配置了知识库（manifest 含 runtime endpoint + app token）时才渲染，避免未接入的实例误调不存在的 skill。

---

## 7. 权限边界

manager 用户侧权限统一由 [`internal/auth/authorizer.go`](../internal/auth/authorizer.go) 判断（谓词
`CanWriteOrgKnowledge` 等），UI 控件可见性与后端校验一致。

| 主体 | 企业知识库 | 实例知识库 | 行业知识库 |
|---|---|---|---|
| 平台管理员 `platform_admin` | 读 + 写（上传 / 删除 / 重解析）| 读 + 写 | 读 + 写（创建 / 重命名 / 上传 / 删除 / 重解析）|
| 企业管理员 `org_admin` | 读 + 写 | 本企业实例：读 + 写 | 无直接管理权限 |
| 企业成员 `org_member` | **只读**（浏览 / 下载）| 自己的实例：读 + 写 | 无直接管理权限 |
| Hermes（app runtime token）| **只读检索** | 读检索 + 写当前实例（`oc-kb add`）| 只读检索所选行业库 |

- 企业成员尝试写企业知识库时，UI 不显示写控件，后端也会返回 `403 KNOWLEDGE_FORBIDDEN`。
- Hermes runtime token 只能读「当前 app + 所属 org + 当前助手版本选择的行业库」、只能写「当前 app」，无法写企业、行业或其它实例。

---

## 8. 部署前置：RAGFlow 模型配置

manager **不自动配置 RAGFlow 的模型供应商**。启用知识库前，管理员需在 RAGFlow 控制台完成：

1. 在 new-api 为 RAGFlow 创建专用 API key；
2. 在 RAGFlow 模型供应商配置中填写 new-api 的 base URL 与该 key（OpenAI-API-Compatible）；
3. 选择解析 / embedding / retrieval 所需模型（如 DeepSeek 系列做 chat、embedding 模型做向量化）；
   dataset 必须设置 embedding 模型，否则 retrieval 会因「Model Name is required」失败；
4. 用 RAGFlow 控制台或 API 验证「上传 → 解析 → retrieval」能跑通后，再启用 oc-manager 知识库。

manager 侧只需在 `config/manager.yaml` 配置 RAGFlow 后端连接（详见 [配置参考](./configuration.md)）：

```yaml
ragflow:
  base_url: "http://ragflow:9380"   # 本地用 compose service name；同机生产用 host.docker.internal；异机用内网/HTTPS
  api_key: "ragflow-..."            # manager 调 RAGFlow HTTP API 的管理凭证，仅存后端配置
  request_timeout: "30s"
  chunk_method: "naive"
```

> RAGFlow 调 new-api 的模型 key 由管理员手工维护，manager 不读取、不保存、不轮换；RAGFlow API key 只保存在
> manager 后端配置，绝不下发给浏览器或 Hermes。

---

## 9. 常见问题排查

| 现象 | 排查方向 |
|---|---|
| 知识库页面报错 / 列表加载失败 | 检查 manager `ragflow.*` 配置、RAGFlow 服务是否可达 |
| 文件一直 `queued` / `running` | 看后台任务是否在跑、RAGFlow 解析队列与模型供应商是否正常 |
| 上传后检索不到 | 确认该文档已 `completed`；确认 dataset 配了 embedding 模型 |
| Hermes 不调用知识库 | 容器内跑 `oc-kb search "测试"` 验证链路；确认 `SOUL.md` 含知识库指引、manifest 含 `knowledge` 配置 |
| Hermes 检索不到企业文档 | 确认文档在「企业」scope 且已解析完成；企业结果排在实例结果之后，但仍会返回 |
| Hermes 返回行业知识库上下文过多 | 检查助手版本关联的行业库数量；每个行业库都会返回最多 `top_k` 条 |
| 解析 `failed` | 列表点「重解析」重新提交；持续失败排查 RAGFlow 侧解析日志 |

---

## 相关文档

- [用户手册](./user-manual.md) — 各角色在 UI 上的逐步操作（平台行业知识库、§2.5 企业级知识库、§2.3 实例知识库 tab）
- [Hermes 容器运行机制](./hermes-container.md) — §7 知识库链路：Hermes → manager runtime API → RAGFlow
- [配置参考](./configuration.md) — `ragflow.*` 配置项
- [架构总览](./architecture.md) — 模块图与数据流
- [设计文档：RAGFlow 替换现有知识库主库](./superpowers/specs/2026-05-26-ragflow-knowledge-design.md) — 完整设计取舍与数据模型
